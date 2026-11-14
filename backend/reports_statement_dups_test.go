package main

import "testing"

func TestDuplicateStatementLines(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "dups-owner@example.com")
	outsider := seedSession(t, a, "dups-other@example.com")
	o := itoa(orgFromList(t, a, owner, `{"name":"Dups","fiscal_year_end":"12-31"}`))
	csv := "date,description,reference,amount\n2026-10-05,Coffee shop,r1,-4.50\n2026-10-05,COFFEE SHOP LTD,r2,-4.50\n2026-10-05,Coffee shop,r1,-4.50\n2026-10-06,Rent,,-100\n2026-11-02,Lunch,a,-9\n2026-11-02,Lunch,b,-9\n"
	rec := do(t, h, "POST", "/api/v1/bank/statements?org_id="+o, csv, owner)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	q := "/api/v1/reports/duplicate-statement-lines?org_id=" + o + "&from=2026-10-01&to=2026-10-31"
	lines := expectStatus(t, do(t, h, "GET", q, "", owner), 200)["lines"].([]any)
	if len(lines) != 2 {
		t.Fatal("want the two differing October lines only", lines)
	}
	expectStatus(t, do(t, h, "GET", q, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/duplicate-statement-lines?org_id="+o+"&from=x&to=2026-10-31", "", owner), 400)
}
