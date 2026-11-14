package main

import (
	"context"
	"testing"
)

// Phase 1e: tracking values live on document lines, the tracking summary reads the posted journal
// lines, and it reconciles to the profit and loss for the same period.

func TestTrackingSplitInvoiceReconcilesToProfitAndLoss(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "track-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Track","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	cat := expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Region","options":["North","South"]}`, owner), 201)
	cats := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)
	list := cats["categories"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	first := list[0].(map[string]any)
	catID := int64(first["id"].(float64))
	opts := first["options"].([]any)
	if len(opts) != 2 {
		t.Fatal(opts)
	}
	north := int64(opts[0].(map[string]any)["id"].(float64))
	south := int64(opts[1].(map[string]any)["id"].(float64))
	_ = cat

	// One invoice, two lines, two different options: the split must survive posting.
	body := `{"org_id":` + o + `,"contact_id":` + cust + `,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[
		{"description":"north work","quantity":1,"unit_price_cents":1000,"tracking":{"` + itoa(catID) + `":` + itoa(north) + `}},
		{"description":"south work","quantity":1,"unit_price_cents":2000,"tracking":{"` + itoa(catID) + `":` + itoa(south) + `}}]}`
	inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/tracking-summary?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want one row per option, got %v", rows)
	}
	byOption := map[string]float64{}
	for _, r := range rows {
		m := r.(map[string]any)
		byOption[m["option"].(string)] = m["income_cents"].(float64)
	}
	if byOption["North"] != 1000 || byOption["South"] != 2000 {
		t.Fatalf("split not counted per line: %v", byOption)
	}
	if got["income_cents"] != float64(3000) || got["untracked_income_cents"] != float64(0) {
		t.Fatalf("totals: %v", got)
	}

	// The same period's profit and loss must agree, both in total and per tracking option.
	pl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	if pl["revenue_cents"] != got["income_cents"] {
		t.Fatalf("tracking summary %v does not reconcile to P&L revenue %v", got["income_cents"], pl["revenue_cents"])
	}
	plTrack := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+o+"&from=2026-10-01&to=2026-10-31&tracking="+itoa(catID), "", owner), 200)
	track := plTrack["tracking"].(map[string]any)
	var columnSum float64
	for _, r := range track["rows"].([]any) {
		m := r.(map[string]any)
		for _, v := range m["amounts"].(map[string]any) {
			columnSum += v.(float64)
		}
		columnSum += m["untracked_cents"].(float64)
	}
	if columnSum != pl["revenue_cents"] {
		t.Fatalf("P&L tracking columns sum to %v, revenue is %v", columnSum, pl["revenue_cents"])
	}
}

func TestTrackingArchivedOptionIsRefusedButHistoryKept(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "track-archive@example.com")
	org := orgFromList(t, a, owner, `{"name":"Archive","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Region","options":["North","South"]}`, owner), 201)
	cats := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)
	first := cats["categories"].([]any)[0].(map[string]any)
	catID := int64(first["id"].(float64))
	opts := first["options"].([]any)
	north := int64(opts[0].(map[string]any)["id"].(float64))

	body := `{"org_id":` + o + `,"contact_id":` + cust + `,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[
		{"description":"work","quantity":1,"unit_price_cents":1000,"tracking":{"` + itoa(catID) + `":` + itoa(north) + `}}]}`
	inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)

	// Archive the option: history keeps its value, a new line cannot choose it.
	expectStatus(t, do(t, h, "PUT", "/api/v1/tracking/options/"+itoa(north), `{"org_id":`+o+`,"name":"North","archived":true}`, owner), 200)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/tracking-summary?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	if rows := got["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["income_cents"] != float64(1000) {
		t.Fatalf("archived option must stay on history: %v", rows)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 422)
}

func TestTrackingCategoriesLimitAndRename(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "track-limit@example.com")
	org := orgFromList(t, a, owner, `{"name":"Limit","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Region","options":["North"]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Team","options":["Alpha"]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Third","options":["X"]}`, owner), 422)
	cats := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)
	list := cats["categories"].([]any)
	if len(list) != 2 {
		t.Fatalf("two active categories expected, got %v", len(list))
	}
	firstID := int64(list[0].(map[string]any)["id"].(float64))
	expectStatus(t, do(t, h, "PUT", "/api/v1/tracking/categories/"+itoa(firstID), `{"org_id":`+o+`,"name":"Region (renamed)","active":true}`, owner), 200)
	renamed := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)
	if renamed["categories"].([]any)[0].(map[string]any)["name"] != "Region (renamed)" {
		t.Fatal("rename did not stick")
	}
}

// The migration maps a document carrying exactly one legacy tag onto every one of its lines and
// leaves a document with two tags unmapped, counting both in tracking_backfill_report.
func TestTrackingBackfillMapsSingleTagOnly(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "track-backfill@example.com")
	org := orgFromList(t, a, owner, `{"name":"Backfill","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	tagA := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/reporting-tags", `{"org_id":`+o+`,"name":"Region","options":["North","South"]}`, owner), 201)["id"].(float64)))
	tagB := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/reporting-tags", `{"org_id":`+o+`,"name":"Team","options":["Alpha","Beta"]}`, owner), 201)["id"].(float64)))
	invoice := func(day string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"`+day+`","due_date":"`+day+`","lines":[{"description":"work","quantity":1,"unit_price_cents":1000}]}`, owner), 201)["id"].(float64))
	}
	single := invoice("2026-10-05")
	double := invoice("2026-10-06")
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(single)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(double)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "PUT", "/api/v1/entities/invoices/"+itoa(single)+"/extras", `{"org_id":`+o+`,"tags":{"`+tagA+`":"North"}}`, owner), 200)
	expectStatus(t, do(t, h, "PUT", "/api/v1/entities/invoices/"+itoa(double)+"/extras", `{"org_id":`+o+`,"tags":{"`+tagA+`":"South","`+tagB+`":"Alpha"}}`, owner), 200)

	ctx := context.Background()
	if _, err := pool.Exec(ctx, `SELECT tracking_backfill()`); err != nil {
		t.Fatal(err)
	}
	var mapped, skipped, lines int64
	if err := pool.QueryRow(ctx, `SELECT documents_mapped, documents_skipped, lines_mapped FROM tracking_backfill_report WHERE kind='invoices'`).Scan(&mapped, &skipped, &lines); err != nil {
		t.Fatal(err)
	}
	if mapped != 1 || skipped != 1 || lines != 1 {
		t.Fatalf("backfill counts: mapped=%d skipped=%d lines=%d", mapped, skipped, lines)
	}
	var singleTracking, doubleTracking string
	if err := pool.QueryRow(ctx, `SELECT tracking::text FROM invoice_lines WHERE org_id=$1 AND invoice_id=$2`, org, single).Scan(&singleTracking); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT tracking::text FROM invoice_lines WHERE org_id=$1 AND invoice_id=$2`, org, double).Scan(&doubleTracking); err != nil {
		t.Fatal(err)
	}
	if singleTracking == "{}" || doubleTracking != "{}" {
		t.Fatalf("single=%s double=%s", singleTracking, doubleTracking)
	}
	// The posted journal line carries the same option, so the report shows it without re-tagging.
	var journalTracking string
	if err := pool.QueryRow(ctx, `SELECT jl.tracking::text FROM journal_lines jl JOIN invoices i ON i.org_id=jl.org_id AND i.journal_id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE i.org_id=$1 AND i.id=$2 AND a.type='revenue'`, org, single).Scan(&journalTracking); err != nil {
		t.Fatal(err)
	}
	if journalTracking != singleTracking {
		t.Fatalf("journal %s line %s", journalTracking, singleTracking)
	}
}
