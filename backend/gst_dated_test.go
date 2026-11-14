package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// gstRefused accepts either refusal status for unsupported_gst; routes differ on 400 vs 422.
func gstRefused(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if (rec.Code != 400 && rec.Code != 422) || !strings.Contains(rec.Body.String(), "unsupported_gst") {
		t.Fatalf("%s: status=%d body=%s, want unsupported_gst refusal", what, rec.Code, rec.Body.String())
	}
}

// TestGSTFollowsDocumentDate covers B2: every tax-producing route uses the registration period that
// covers its own date, so a past registered period still works after deregistration and a date outside
// registration is refused.
func TestGSTFollowsDocumentDate(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-dated@example.com")
	org := orgFromList(t, a, owner, `{"name":"Dated GST","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := seedContact(t, a, org, owner)
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	cash := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1015","name":"Petty cash","type":"asset"}`, owner), 201))
	supp := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201))
	exp := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Cost","type":"expense"}`, owner), 201))

	invoice := func(date string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"` + date + `","due_date":"` + date + `","lines":[{"description":"Work","revenue_account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	}
	quote := func(date string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"quote_date":"` + date + `","expiry_date":"` + date + `","lines":[{"description":"Work","revenue_account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	}
	receipt := func(date, key string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"receipt_date":"` + date + `","payment_mode":"cash","deposit_account_id":` + itoa(cash) + `,"idempotency_key":"` + key + `","lines":[{"description":"Sale","account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	}
	credit := func(date string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"credit_date":"` + date + `","lines":[{"description":"Refund","account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	}
	recurringInvoice := func(date string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"name":"Monthly","frequency":"monthly","start_date":"` + date + `","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	}
	recurringBill := func(date string) string {
		return `{"org_id":` + o + `,"name":"Monthly bill","repeat_every":1,"repeat_unit":"month","start_date":"` + date + `","bill":{"org_id":` + o + `,"contact_id":` + itoa(supp) + `,"supplier_reference":"RB-1","bill_date":"` + date + `","due_date":"` + date + `","lines":[{"account_id":` + itoa(exp) + `,"description":"Cost","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}}`
	}

	// Before registration starts, SR is refused on every sales route.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice("2021-12-15"), owner), 422)
	gstRefused(t, do(t, h, "POST", "/api/v1/quotes", quote("2021-12-15"), owner), "quote before start")
	gstRefused(t, do(t, h, "POST", "/api/v1/sales-receipts", receipt("2021-12-15", "before-start"), owner), "sales receipt before start")
	gstRefused(t, do(t, h, "POST", "/api/v1/credit-notes", credit("2021-12-15"), owner), "credit note before start")
	gstRefused(t, do(t, h, "POST", "/api/v1/recurring", recurringInvoice("2021-12-15"), owner), "recurring invoice before start")
	gstRefused(t, do(t, h, "POST", "/api/v1/recurring-bills", recurringBill("2021-12-15"), owner), "recurring bill before start")

	// Inside the registered period the dated rate applies (8% in 2023).
	if m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice("2023-06-15"), owner), 201); m["gst_cents"] != float64(800) {
		t.Fatalf("2023 invoice gst %v, want 800", m["gst_cents"])
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", quote("2023-06-15"), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", receipt("2023-06-15", "in-period"), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", credit("2023-06-15"), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", recurringInvoice("2023-06-15"), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", recurringBill("2023-06-15"), owner), 201)

	// Deregistered from 2026-01-01: past periods keep working, later dates are refused.
	if _, err := pool.Exec(t.Context(), `UPDATE gst_registrations SET effective_to='2025-12-31' WHERE org_id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice("2024-06-15"), owner), 201); m["gst_cents"] != float64(900) {
		t.Fatalf("past-period invoice gst %v, want 900", m["gst_cents"])
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", quote("2024-06-15"), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice("2026-03-01"), owner), 422)
	gstRefused(t, do(t, h, "POST", "/api/v1/quotes", quote("2026-03-01"), owner), "quote after dereg")
	gstRefused(t, do(t, h, "POST", "/api/v1/sales-receipts", receipt("2026-03-01", "after-end"), owner), "sales receipt after dereg")
	gstRefused(t, do(t, h, "POST", "/api/v1/credit-notes", credit("2026-03-01"), owner), "credit note after dereg")
	gstRefused(t, do(t, h, "POST", "/api/v1/recurring", recurringInvoice("2026-03-01"), owner), "recurring invoice after dereg")
	gstRefused(t, do(t, h, "POST", "/api/v1/recurring-bills", recurringBill("2026-03-01"), owner), "recurring bill after dereg")

	// F5 for a past registered period is still accepted; a wholly unregistered period is not.
	f5 := func(start, end string) string {
		return `{"org_id":` + o + `,"period_start":"` + start + `","period_end":"` + end + `","lines":[{"kind":"sale","code":"SR","net_cents":100,"gst_cents":9}]}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5", f5("2024-01-01", "2024-03-31"), owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5", f5("2026-01-01", "2026-03-31"), owner), 422)
}
