package main

import (
	"context"
	"strings"
	"testing"
)

// TestSavedReportsAndBlankReport covers server-side saved reports: a draft can be saved, published,
// archived, and a blank report stores its own columns and rows. Deleting a published report is refused.
func TestSavedReportsAndBlankReport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "saved-reports@example.com")
	org := orgFromList(t, a, owner, `{"name":"Saved reports","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	saved := expectStatus(t, do(t, h, "POST", "/api/v1/saved-reports", `{"org_id":`+o+`,"title":"Monthly P&L","report_key":"profit-loss","params":{"from":"2026-03-01","to":"2026-03-31"},"favourite":true}`, owner), 201)
	id := itoa(int64(saved["id"].(float64)))
	expectStatus(t, do(t, h, "PUT", "/api/v1/saved-reports/"+id, `{"org_id":`+o+`,"title":"Monthly P&L","report_key":"profit-loss","status":"published","params":{"from":"2026-03-01","to":"2026-03-31"}}`, owner), 200)
	expectStatus(t, do(t, h, "DELETE", "/api/v1/saved-reports/"+id+"?org_id="+o, "", owner), 409)

	blank := expectStatus(t, do(t, h, "POST", "/api/v1/saved-reports", `{"org_id":`+o+`,"title":"My blank","report_key":"blank","layout":{"columns":["Item","Amount"],"rows":[["Rent","1200"]]}}`, owner), 201)
	blankID := int64(blank["id"].(float64))
	rep := expectStatus(t, do(t, h, "GET", "/api/v1/reports/blank?org_id="+o+"&saved_id="+itoa(blankID), "", owner), 200)
	if rep["title"] != "My blank" || len(rep["columns"].([]any)) != 2 || len(rep["rows"].([]any)) != 1 {
		t.Fatalf("blank report layout %v", rep)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/saved-reports?org_id="+o+"&report_key=profit-loss&status=published", "", owner), 200)
	rows := list["saved_reports"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["favourite"] != true {
		t.Fatalf("published/favourite report not listed: %v", list)
	}
	updated := expectStatus(t, do(t, h, "PUT", "/api/v1/saved-reports/"+id, `{"org_id":`+o+`,"title":"Monthly P&L","report_key":"profit-loss","status":"published","params":{"from":"2026-03-01","to":"2026-03-31"},"favourite":false}`, owner), 200)
	if updated["id"] == nil {
		t.Fatalf("favourite update response %v", updated)
	}
	list = expectStatus(t, do(t, h, "GET", "/api/v1/saved-reports?org_id="+o+"&report_key=profit-loss&status=published", "", owner), 200)
	if list["saved_reports"].([]any)[0].(map[string]any)["favourite"] != false {
		t.Fatalf("favourite not cleared: %v", list)
	}
}

// TestBackordersRecordShortfallAndFulfil covers the stock promise model: an invoice for more than is on
// hand still posts, the shortfall becomes an open backorder, and a later bill covers it oldest first.
func TestBackordersRecordShortfallAndFulfil(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "backorder-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Backorder co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Backorder customer"}`, owner), 201)["id"].(float64)))
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Backorder supplier"}`, owner), 201)["id"].(float64)))
	rev := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4197","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	exp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"5197","name":"Costs","type":"expense"}`, owner), 201)["id"].(float64)))
	item := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"BO-1","selling_price_cents":1000,"cost_price_cents":400,"sales_account_id":`+rev+`,"purchase_account_id":`+exp+`,"track_inventory":true}`, owner), 201)["id"].(float64)))

	// Two on hand, five sold: three are promised.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", `{"org_id":`+o+`,"item_id":`+item+`,"adjustment_date":"2026-03-01","qty_delta":2,"unit_cost_cents":400,"reason":"Opening count"}`, owner), 201)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-02","due_date":"2026-03-31","lines":[{"description":"Widgets","revenue_account_id":`+rev+`,"item_id":`+item+`,"quantity":5,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	open := expectStatus(t, do(t, h, "GET", "/api/v1/backorders?org_id="+o, "", owner), 200)["backorders"].([]any)
	if len(open) != 1 || open[0].(map[string]any)["open_qty"].(float64) != 3 {
		t.Fatalf("backorder after short sale %v", open)
	}

	// A bill for four brings stock in; three close the promise, one stays on hand.
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"BO-R1","bill_date":"2026-03-05","due_date":"2026-03-31","lines":[{"description":"Widgets","item_id":`+item+`,"quantity":4,"unit_price_cents":400}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	if got := expectStatus(t, do(t, h, "GET", "/api/v1/backorders?org_id="+o, "", owner), 200)["backorders"].([]any); len(got) != 0 {
		t.Fatalf("promise not closed by the later bill: %v", got)
	}
	closed := expectStatus(t, do(t, h, "GET", "/api/v1/backorders?org_id="+o+"&status=all", "", owner), 200)["backorders"].([]any)
	if len(closed) != 1 || closed[0].(map[string]any)["fulfilled_qty"].(float64) != 3 {
		t.Fatalf("backorder history %v", closed)
	}
	report := expectStatus(t, do(t, h, "GET", "/api/v1/reports/backorders?org_id="+o+"&status=all", "", owner), 200)
	if len(report["backorders"].([]any)) != 1 {
		t.Fatalf("backorder report %v", report)
	}
}

// TestAssetRevaluationAndRollback covers fixed-asset revaluation: a gain posts to the reserve and lifts
// the asset cost, and a rollback reverses both without editing the original journal.
func TestAssetRevaluationAndRollback(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "revaluation@example.com")
	org := orgFromList(t, a, owner, `{"name":"Revaluation co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	asset := expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Forklift","acquired_on":"2026-01-01","cost_cents":100000,"useful_life_months":60,"depreciation_method":"straight_line","averaging_rule":"full_month","residual_value_cents":0}`, owner), 201)
	assetID := itoa(int64(asset["id"].(float64)))
	rev := expectStatus(t, do(t, h, "POST", "/api/v1/assets/"+assetID+"/revalue", `{"org_id":`+o+`,"revalued_on":"2026-03-31","amount_cents":25000,"note":"Independent valuation"}`, owner), 201)
	revID := int64(rev["id"].(float64))
	if rows := expectStatus(t, do(t, h, "GET", "/api/v1/assets/"+assetID+"/revaluations?org_id="+o, "", owner), 200)["revaluations"].([]any); len(rows) != 1 || rows[0].(map[string]any)["reversed"].(bool) {
		t.Fatalf("revaluation list %v", rows)
	}
	var cost int64
	if err := a.db.QueryRow(context.Background(), `SELECT cost_cents FROM fixed_assets WHERE org_id=$1 AND id=$2`, org, int64(asset["id"].(float64))).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != 125000 {
		t.Fatalf("asset cost after revaluation %d, want 125000", cost)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/"+assetID+"/revaluations/"+itoa(revID)+"/rollback", `{"org_id":`+o+`,"reversed_on":"2026-04-01"}`, owner), 200)
	if err := a.db.QueryRow(context.Background(), `SELECT cost_cents FROM fixed_assets WHERE org_id=$1 AND id=$2`, org, int64(asset["id"].(float64))).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost != 100000 {
		t.Fatalf("asset cost after rollback %d, want 100000", cost)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/"+assetID+"/revaluations/"+itoa(revID)+"/rollback", `{"org_id":`+o+`,"reversed_on":"2026-04-02"}`, owner), 409)
}

// TestProjectTasksAndRolesAndPermissionMatrix covers the remaining Phase 5 controls: a per-project task
// catalogue, per-person project roles, and a feature matrix that can take away one area without
// touching the others.
func TestProjectTasksAndRolesAndPermissionMatrix(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "project-roles@example.com")
	org := orgFromList(t, a, owner, `{"name":"Project roles","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Project customer"}`, owner), 201)["id"].(float64)))
	project := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Fit out"}`, owner), 201)["id"].(float64)))

	task := expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+project+"/tasks", `{"org_id":`+o+`,"name":"Install","rate_cents":9000}`, owner), 201)
	if len(expectStatus(t, do(t, h, "GET", "/api/v1/projects/"+project+"/tasks?org_id="+o, "", owner), 200)["tasks"].([]any)) != 1 {
		t.Fatal("task not listed")
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+project+"/tasks", `{"org_id":`+o+`,"name":"Install","rate_cents":9000}`, owner), 409)
	expectStatus(t, do(t, h, "PATCH", "/api/v1/project-tasks/"+itoa(int64(task["id"].(float64))), `{"org_id":`+o+`,"name":"Install","rate_cents":9500,"active":false}`, owner), 200)

	// A person joins the project as lead; someone outside the company cannot.
	member := seedSession(t, a, "project-lead@example.com")
	var leadID int64
	if err := a.db.QueryRow(context.Background(), `SELECT id FROM users WHERE email='project-lead@example.com'`).Scan(&leadID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member')`, org, leadID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+project+"/members", `{"org_id":`+o+`,"user_id":`+itoa(leadID)+`,"role":"lead"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+project+"/members", `{"org_id":`+o+`,"user_id":999999,"role":"member"}`, owner), 422)
	roles := expectStatus(t, do(t, h, "GET", "/api/v1/projects/"+project+"/members?org_id="+o, "", owner), 200)["members"].([]any)
	if len(roles) != 1 || roles[0].(map[string]any)["role"] != "lead" {
		t.Fatalf("project roles %v", roles)
	}

	// The matrix: remove sales and contacts, keep reports, then restore.
	expectStatus(t, do(t, h, "PATCH", "/api/v1/members/"+itoa(leadID)+"/permissions", `{"org_id":`+o+`,"permissions":{"sales":false,"contacts":false,"reports":true}}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Blocked"}`, member), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"X","quantity":1,"unit_price_cents":100}]}`, member), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2026-12-31", "", member), 200)
	expectStatus(t, do(t, h, "PATCH", "/api/v1/members/"+itoa(leadID)+"/permissions", `{"org_id":`+o+`,"permissions":{"sales":true,"contacts":true}}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Allowed again"}`, member), 201)
}

// TestApproveAndEmailPostsAndSends checks the approve-and-send path: a draft invoice is posted, the
// customer email is sent, and the reply names both outcomes.
func TestApproveAndEmailPostsAndSends(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	owner := seedSession(t, a, "approve-send@example.com")
	org := orgFromList(t, a, owner, `{"name":"Approve send","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer","email":"buyer@example.com"}`, owner), 201)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(int64(cust["id"].(float64)))+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":2500}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))
	res := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/approve-and-email", `{"org_id":`+o+`}`, owner), 200)
	if res["emailed"] != true || res["status"] != "posted" {
		t.Fatalf("approve-and-email reply %v", res)
	}
	if sink.sent != 1 || !strings.Contains(sink.lastSubject(t), "Invoice") {
		t.Fatalf("email not sent: %d %q", sink.sent, sink.lastSubject(t))
	}
	// A second call on a posted document emails again but does not post twice.
	again := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/approve-and-email", `{"org_id":`+o+`,"to":"other@example.com"}`, owner), 200)
	if again["number"].(float64) != res["number"].(float64) {
		t.Fatalf("second send changed the number: %v vs %v", again, res)
	}
}
