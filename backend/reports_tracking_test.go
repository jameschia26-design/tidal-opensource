package main

import "testing"

// The tracking summary counts posted amounts per line, so an untagged line stays untracked even when
// another line on the same document carries an option.
func TestTrackingSummary(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "track-owner@example.com")
	outsider := seedSession(t, a, "track-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Track","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Region","options":["North","South"]}`, owner), 201)
	cats := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)
	first := cats["categories"].([]any)[0].(map[string]any)
	cat := itoa(int64(first["id"].(float64)))
	opts := first["options"].([]any)
	north := itoa(int64(opts[0].(map[string]any)["id"].(float64)))
	south := itoa(int64(opts[1].(map[string]any)["id"].(float64)))

	// Two invoices on two dates, each line tagged; a third invoice left untagged.
	line := func(price int, option string) string {
		if option == "" {
			return `{"description":"work","quantity":1,"unit_price_cents":` + itoa(int64(price)) + `}`
		}
		return `{"description":"work","quantity":1,"unit_price_cents":` + itoa(int64(price)) + `,"tracking":{"` + cat + `":` + option + `}}`
	}
	make := func(day string, lines string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"`+day+`","due_date":"`+day+`","lines":[`+lines+`]}`, owner), 201)["id"].(float64))
	}
	idA := make("2026-10-05", line(1000, north))
	idB := make("2026-10-06", line(1000, south))
	idC := make("2026-10-07", line(1000, ""))
	for _, id := range []int64{idA, idB, idC} {
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
	}
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/tracking-summary?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 2 || got["income_cents"] != float64(3000) || got["untracked_income_cents"] != float64(1000) {
		t.Fatal(rows, got)
	}
	firstRow := rows[0].(map[string]any)
	if firstRow["category"] != "Region" || firstRow["option"] != "North" || firstRow["income_cents"] != float64(1000) || firstRow["expense_cents"] != float64(0) {
		t.Fatal(firstRow)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/tracking-summary?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/tracking-summary?org_id="+o+"&from=2026-10-31&to=2026-10-01", "", owner), 400)
}
