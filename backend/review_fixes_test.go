package main

import (
	"strings"
	"testing"
)

func TestReviewQuoteGSTConversionAndPDF(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "review-quote@example.com")
	org := orgFromList(t, a, owner, `{"name":"Review quote","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := itoa(seedContact(t, a, org, owner))
	q := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", `{"org_id":`+o+`,"contact_id":`+cust+`,"quote_date":"2023-12-01","expiry_date":"2024-02-01","lines":[{"description":"Taxed","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
	path := "/api/v1/quotes/" + itoa(int64(q["id"].(float64)))
	pdf := do(t, h, "GET", "/api/v1/documents/quotes/"+itoa(int64(q["id"].(float64)))+"/pdf?org_id="+o, "", owner)
	for _, want := range []string{"GST SGD 8.00", "Total SGD 108.00", "Total includes GST"} {
		if pdf.Code != 200 || !strings.Contains(pdf.Body.String(), want) {
			t.Fatalf("quote PDF missing %q: %d", want, pdf.Code)
		}
	}
	for _, stage := range []string{"sent", "accepted"} {
		expectStatus(t, do(t, h, "POST", path+"/stage", `{"org_id":`+o+`,"stage":"`+stage+`"}`, owner), 200)
	}
	converted := expectStatus(t, do(t, h, "POST", path+"/convert", `{"org_id":`+o+`,"invoice_date":"2024-01-01","due_date":"2024-01-31"}`, owner), 201)
	var total, gst, rate, lineGST int64
	if err := pool.QueryRow(t.Context(), `SELECT i.total_cents,i.gst_cents,l.gst_rate_bp,l.gst_cents FROM invoices i JOIN invoice_lines l ON l.org_id=i.org_id AND l.invoice_id=i.id WHERE i.org_id=$1 AND i.id=$2`, org, int64(converted["invoice_id"].(float64))).Scan(&total, &gst, &rate, &lineGST); err != nil {
		t.Fatal(err)
	}
	if total != 10900 || gst != 900 || rate != 900 || lineGST != 900 {
		t.Fatal(total, gst, rate, lineGST)
	}
}

func TestReviewRecurringSkipAndRetry(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "review-recurring@example.com")
	org := orgFromList(t, a, owner, `{"name":"Review recurring","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := itoa(seedContact(t, a, org, owner))
	create := func(code string) float64 {
		t.Helper()
		v := expectStatus(t, do(t, h, "POST", "/api/v1/recurring", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Test","frequency":"monthly","start_date":"2026-01-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000,"gst_code":"`+code+`"}]}`, owner), 201)
		return v["id"].(float64)
	}
	sr, plain := create("SR"), create("NONE")
	// Deregister through the dated history: GST now follows the document date.
	if _, err := pool.Exec(t.Context(), `UPDATE gst_registrations SET effective_to='2025-12-31' WHERE org_id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	run := func() map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"2026-01-01"}`, owner), 200)
	}
	first := run()
	if ids := first["skipped_template_ids"].([]any); len(ids) != 1 || ids[0] != sr || len(first["created_invoice_ids"].([]any)) != 1 {
		t.Fatal(first)
	}
	var srDate, plainDate string
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT next_run::text FROM recurring_templates WHERE id=$1),(SELECT next_run::text FROM recurring_templates WHERE id=$2)`, int64(sr), int64(plain)).Scan(&srDate, &plainDate); err != nil {
		t.Fatal(err)
	}
	if srDate != "2026-01-01" || plainDate != "2026-02-01" {
		t.Fatal(srDate, plainDate)
	}
	again := run()
	if len(again["created_invoice_ids"].([]any)) != 0 || len(again["skipped_template_ids"].([]any)) != 1 {
		t.Fatal(again)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO gst_registrations(org_id,effective_from) VALUES($1,'2026-01-01')`, org); err != nil {
		t.Fatal(err)
	}
	retry := run()
	if len(retry["created_invoice_ids"].([]any)) != 1 || len(retry["skipped_template_ids"].([]any)) != 0 {
		t.Fatal(retry)
	}
}

func TestReviewCreditNotesGSTReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "review-credit@example.com")
	org := orgFromList(t, a, owner, `{"name":"Review credit","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := itoa(seedContact(t, a, org, owner))
	rev := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	create := func(date string) string {
		t.Helper()
		m := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+cust+`,"credit_date":"`+date+`","lines":[{"description":"Refund","account_id":`+rev+`,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
		return itoa(int64(m["id"].(float64)))
	}
	open := create("2026-03-10")
	create("2026-03-11")
	outside := create("2026-04-01")
	for _, id := range []string{open, outside} {
		expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+id+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)
	}
	query := "?org_id=" + o + "&from=2026-03-01&to=2026-03-31"
	audit := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-audit"+query, "", owner), 200)["rows"].([]any)
	if len(audit) != 1 {
		t.Fatal(audit)
	}
	row := audit[0].(map[string]any)
	if row["kind"] != "sale" || row["gst_code"] != "SR" || row["amount_cents"] != float64(-10000) || row["gst_cents"] != float64(-900) {
		t.Fatal(row)
	}
	tax := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax"+query, "", owner), 200)
	if tax["taxable_cents"] != float64(-10000) || tax["tax_cents"] != float64(-900) {
		t.Fatal(tax)
	}
}
