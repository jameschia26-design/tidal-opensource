package main

import (
	"net/http"
	"testing"
)

// Sales receipts: GST is optional per line, only for GST-registered companies, and reaches the ledger and tax reports.
func TestSalesReceiptGST(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-receipt@example.com")
	plainOwner := seedSession(t, a, "gst-receipt-plain@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST Receipt Test","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	plain := orgFromList(t, a, plainOwner, `{"name":"Plain Receipt Test","fiscal_year_end":"12-31"}`)
	setup := func(o int64, tok string) (cust, rev, cash int64) {
		cust = seedContact(t, a, o, tok)
		rev = int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(o)+`,"code":"4100","name":"Sales","type":"revenue"}`, tok), 201)["id"].(float64))
		cash = int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(o)+`,"code":"1015","name":"Petty cash","type":"asset"}`, tok), 201)["id"].(float64))
		return
	}
	cust, rev, cash := setup(org, owner)
	pcust, prev, pcash := setup(plain, plainOwner)
	body := func(o, c, r, d int64, key, code, extra string) string {
		return `{"org_id":` + itoa(o) + `,"contact_id":` + itoa(c) + `,"receipt_date":"2026-10-30","payment_mode":"cash","deposit_account_id":` + itoa(d) + `,"idempotency_key":"` + key + `","lines":[{"description":"Test","account_id":` + itoa(r) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"` + code + `"}]` + extra + `}`
	}
	refused := func(b, tok string) {
		t.Helper()
		if m := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", b, tok), http.StatusUnprocessableEntity); m["error"] != "unsupported_gst" {
			t.Fatal(m)
		}
	}
	refused(body(plain, pcust, prev, pcash, "gst-plain-01", "SR", ""), plainOwner) // not registered
	refused(body(org, cust, rev, cash, "gst-bad-0001", "ZZ", ""), owner)           // unknown code
	refused(body(org, cust, rev, cash, "gst-disc-001", "SR", `,"commercial":{"discount_type":"flat","discount_cents":100}`), owner)

	// GST is optional: NONE and omitted codes keep the old no-tax behaviour in a registered company.
	if m := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(org, cust, rev, cash, "gst-none-001", "NONE", ""), owner), 201); m["total_cents"] != float64(10000) {
		t.Fatal(m)
	}
	made := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(org, cust, rev, cash, "gst-sr-00001", "SR", ""), owner), 201)
	if made["total_cents"] != float64(10900) {
		t.Fatal(made)
	}
	if again := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(org, cust, rev, cash, "gst-sr-00001", "SR", ""), owner), 200); again["id"] != made["id"] || again["total_cents"] != float64(10900) {
		t.Fatal(again)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", body(org, cust, rev, cash, "gst-sr-00001", "NONE", ""), owner), 409)

	var bank, income, output int64
	q := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	q(&bank, `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, cash)
	q(&income, `SELECT COALESCE(sum(credit_cents-debit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, rev)
	q(&output, `SELECT COALESCE(sum(l.credit_cents-l.debit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.code='2300'`, org)
	if bank != 20900 || income != 20000 || output != 900 {
		t.Fatalf("deposit %d income %d output %d", bank, income, output)
	}
	var tampered int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM sales_receipts WHERE org_id=$1 AND total_cents=quote_line_subtotal(lines)+quote_line_gst(lines)`, org).Scan(&tampered); err != nil || tampered != 2 {
		t.Fatal(tampered, err)
	}

	o := itoa(org)
	audit := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-audit?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)["rows"].([]any)
	var sr map[string]any
	for _, r := range audit {
		if r := r.(map[string]any); r["gst_code"] == "SR" {
			sr = r
		}
	}
	if len(audit) != 2 || sr == nil || sr["kind"] != "sale" || sr["number"] != "SR-00002" || sr["gst_cents"] != float64(900) || sr["amount_cents"] != float64(10000) {
		t.Fatal(audit)
	}
	tax := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	if tax["taxable_cents"] != float64(20000) || tax["tax_cents"] != float64(900) {
		t.Fatal(tax)
	}
}
