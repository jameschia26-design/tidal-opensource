package main

import "testing"

// TestRoadmapExtraEndpoints covers the small parity endpoints added for contact defaults, company
// targets, project estimates, bulk tax rates and the setup guide. Each one writes through the same
// permission checks as the rest of the books.
func TestRoadmapExtraEndpoints(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "roadmap-extra@example.com")
	org := orgFromList(t, a, owner, `{"name":"Roadmap extra","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	customer := seedContact(t, a, org, owner)
	plainCustomer := seedContact(t, a, org, owner)
	acc := func(code, name, typ string) string {
		return itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"`+code+`","name":"`+name+`","type":"`+typ+`"}`, owner), 201)["id"].(float64)))
	}
	rev, exp := acc("4198", "Sales", "revenue"), acc("5198", "Costs", "expense")

	// Contact defaults are stored and returned.
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Defaults Ltd","sales_treatment":"inclusive","sales_tax_code":"SR","purchase_treatment":"exclusive","purchase_tax_code":"TX"}`, owner), 201)
	if contact["sales_tax_code"] != "SR" || contact["sales_treatment"] != "inclusive" {
		t.Fatalf("contact defaults not saved: %v", contact)
	}

	// Project estimates can be created and patched.
	project := expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+itoa(customer)+`,"name":"Estimate job","estimated_cost_cents":100000,"estimated_charge_cents":250000}`, owner), 201)
	pid := itoa(int64(project["id"].(float64)))
	expectStatus(t, do(t, h, "PATCH", "/api/v1/projects/"+pid, `{"org_id":`+o+`,"estimated_cost_cents":120000,"estimated_charge_cents":300000}`, owner), 200)

	// Bulk account tax rate applies to every chosen account.
	expectStatus(t, do(t, h, "POST", "/api/v1/accounts/bulk-tax-rate", `{"org_id":`+o+`,"account_ids":[`+exp+`],"tax_rate_code":"TX"}`, owner), 200)

	// Company time zone round-trips and rejects an unknown zone.
	expectStatus(t, do(t, h, "PUT", "/api/v1/company-timezone", `{"org_id":`+o+`,"timezone":"Asia/Tokyo"}`, owner), 200)
	if tz := expectStatus(t, do(t, h, "GET", "/api/v1/company-timezone?org_id="+o, "", owner), 200)["timezone"]; tz != "Asia/Tokyo" {
		t.Fatalf("timezone = %v", tz)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/company-timezone", `{"org_id":`+o+`,"timezone":"Not/AZone"}`, owner), 400)

	// Company scorecard targets round-trip.
	expectStatus(t, do(t, h, "PUT", "/api/v1/scorecard-targets", `{"org_id":`+o+`,"targets":{"net_margin_bp":1200,"debtor_days":30}}`, owner), 200)
	targets := expectStatus(t, do(t, h, "GET", "/api/v1/scorecard-targets?org_id="+o, "", owner), 200)["targets"].(map[string]any)
	if targets["net_margin_bp"].(float64) != 1200 || targets["debtor_days"].(float64) != 30 {
		t.Fatalf("targets = %v", targets)
	}

	// Setup progress counts the company and accounts already created.
	progress := expectStatus(t, do(t, h, "GET", "/api/v1/setup-progress?org_id="+o, "", owner), 200)
	if progress["total"].(float64) != 8 || progress["done"].(float64) < 1 {
		t.Fatalf("progress = %v", progress)
	}
	outsider := seedSession(t, a, "notifications-outsider@example.com")
	ownEvents := expectStatus(t, do(t, h, "GET", "/api/v1/notifications?org_id="+o, "", owner), 200)["notifications"].([]any)
	if len(ownEvents) == 0 {
		t.Fatal("company notifications should include events written by this workflow")
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/notifications?org_id="+o, "", outsider), 403)
	member := seedSession(t, a, "notifications-member@example.com")
	var memberID int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM users WHERE email='notifications-member@example.com'`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member')`, org, memberID); err != nil {
		t.Fatal(err)
	}
	memberEvents := expectStatus(t, do(t, h, "GET", "/api/v1/notifications?org_id="+o, "", member), 200)["notifications"].([]any)
	if len(memberEvents) != len(ownEvents) {
		t.Fatalf("company activity differs for member: %d != %d", len(memberEvents), len(ownEvents))
	}

	// A draft invoice can be submitted for approval, then posted.
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(plainCustomer)+`,"invoice_date":"2026-02-01","due_date":"2026-02-10","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000,"revenue_account_id":`+rev+`}]}`, owner), 201)
	iid := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+iid+"/submit", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+iid+"/post?org_id="+o, "", owner), 200)
}
