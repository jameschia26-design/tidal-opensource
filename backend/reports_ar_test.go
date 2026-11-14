package main

import (
	"testing"
)

func TestAgingReportBucketsAndBalances(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "aging-owner@example.com")
	outsider := seedSession(t, a, "aging-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Aging co","fiscal_year_end":"12-31"}`)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Slow payer"}`, owner), 201)
	cid := int64(c["id"].(float64))
	post := func(due string, amount int64) int64 {
		inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-01-01","due_date":"`+due+`","lines":[{"description":"Work","quantity":1,"unit_price_cents":`+itoa(amount)+`}]}`, owner), 201)
		id := int64(inv["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+itoa(org), "", owner), 200)
		return id
	}
	// As of 2026-10-29: due 2026-11-15 is current; 2026-10-01 is 28 days past due; 2026-08-01 is 89 days past due.
	post("2026-11-15", 10000)
	credited := post("2026-10-01", 20000)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(credited)+"/credit-notes", `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-20","reason":"Correction","amount_cents":5000}`, owner), 201)
	paid := post("2026-08-01", 30000)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(paid)+"/payments", `{"org_id":`+itoa(org)+`,"amount_cents":30000,"payment_date":"2026-08-10","idempotency_key":"aging-pay"}`, owner), 201)
	before := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-08-09", "", owner), 200)
	if len(before["rows"].([]any)) != 3 || before["totals_by_bucket"].(map[string]any)["current"] != float64(30000) || before["totals_by_bucket"].(map[string]any)["1-30"] != float64(30000) {
		t.Fatalf("future payment and credit must not reduce earlier balances: %v", before)
	}
	future := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-11-01","due_date":"2026-11-01","lines":[{"description":"Future work","quantity":1,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(future["id"].(float64)))+"/post?org_id="+itoa(org), "", owner), 200)
	filt := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-08-09&contact=slow", "", owner), 200)
	if len(filt["rows"].([]any)) != 3 {
		t.Fatalf("contact filter must match case-insensitively: %v", filt)
	}
	none := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-08-09&contact=nobody-here", "", owner), 200)
	if len(none["rows"].([]any)) != 0 {
		t.Fatalf("contact filter must exclude non-matching names: %v", none)
	}
	rep := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-10-29&basis=invoice&periods=12&period_length=30", "", owner), 200)
	rows := rep["rows"].([]any)
	if len(rows) != 2 {
		t.Fatal("paid invoice still listed", rows)
	}
	totals := rep["totals_by_bucket"].(map[string]any)
	if totals["301-330"] != float64(25000) {
		t.Fatal(totals)
	}
	row := rows[0].(map[string]any)
	if row["invoice_date"] != "2026-01-01" || row["bucket_amounts_cents"].(map[string]any)["301-330"] != float64(15000) {
		t.Fatalf("invoice-date aging and per-document bucket mismatch: %v", row)
	}
	for _, query := range []string{"basis=created", "periods=13", "period_length=0"} {
		expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-10-29&"+query, "", owner), 400)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=2026-10-29", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+itoa(org)+"&as_of=bad", "", owner), 400)
	pay := expectStatus(t, do(t, h, "GET", "/api/v1/reports/payables-aging?org_id="+itoa(org)+"&as_of=2026-10-29", "", owner), 200)
	if len(pay["rows"].([]any)) != 0 {
		t.Fatal("no bills should be listed", pay)
	}
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Supplier"}`, owner), 201)
	account := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6300","name":"Services","type":"expense"}`, owner), 201)
	bill := func(reference, date, due string, amount int64) int64 {
		b := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(int64(supplier["id"].(float64)))+`,"supplier_reference":"`+reference+`","bill_date":"`+date+`","due_date":"`+due+`","lines":[{"account_id":`+itoa(int64(account["id"].(float64)))+`,"description":"Service","quantity":1,"unit_price_cents":`+itoa(amount)+`}]}`, owner), 201)
		id := int64(b["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(id)+"/post?org_id="+itoa(org), "", owner), 200)
		return id
	}
	bid := bill("B-1", "2026-08-01", "2026-08-20", 12000)
	bill("B-2", "2026-11-01", "2026-11-20", 5000)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bid)+"/payments", `{"org_id":`+itoa(org)+`,"amount_cents":4000,"payment_date":"2026-10-10","idempotency_key":"aging-bill-pay"}`, owner), 201)
	beforeBillPayment := expectStatus(t, do(t, h, "GET", "/api/v1/reports/payables-aging?org_id="+itoa(org)+"&as_of=2026-10-01", "", owner), 200)
	if len(beforeBillPayment["rows"].([]any)) != 1 || beforeBillPayment["totals_by_bucket"].(map[string]any)["31-60"] != float64(12000) {
		t.Fatal("future bill and payment must be excluded", beforeBillPayment)
	}
	afterBillPayment := expectStatus(t, do(t, h, "GET", "/api/v1/reports/payables-aging?org_id="+itoa(org)+"&as_of=2026-10-29", "", owner), 200)
	if len(afterBillPayment["rows"].([]any)) != 1 || afterBillPayment["totals_by_bucket"].(map[string]any)["61-90"] != float64(8000) {
		t.Fatal("payment must reduce payable balance", afterBillPayment)
	}
}

// TestAgingIncludesUnappliedCreditsAndPercentages checks that a customer credit note which has not
// been applied to an invoice appears as a negative document row, reduces the bucket total, and that
// the percentage row is a share of the resulting total rather than of the invoices alone.
func TestAgingIncludesUnappliedCreditsAndPercentages(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "aging-credits@example.com")
	org := orgFromList(t, a, owner, `{"name":"Aging credits","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cid := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Credit holder"}`, owner), 201)["id"].(float64))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-10-01","due_date":"2026-10-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	rev := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	credit := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+itoa(cid)+`,"credit_number":"CN-1","credit_date":"2026-10-20","lines":[{"description":"Returned goods","account_id":`+rev+`,"quantity":1,"unit_price_cents":3000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+itoa(int64(credit["id"].(float64)))+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)

	rep := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+o+"&as_of=2026-10-29", "", owner), 200)
	rows := rep["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want invoice and credit rows, got %v", rows)
	}
	var creditRow map[string]any
	for _, r := range rows {
		row := r.(map[string]any)
		if row["balance_cents"].(float64) < 0 {
			creditRow = row
		}
	}
	if creditRow == nil || creditRow["balance_cents"].(float64) != -3000 || creditRow["document"] != "Credit CN-1" {
		t.Fatalf("unapplied credit row %v", creditRow)
	}
	totals := rep["totals_by_bucket"].(map[string]any)
	if totals["1-30"] != float64(7000) {
		t.Fatalf("bucket total must net the credit: %v", totals)
	}
	percent := rep["percent_by_bucket"].(map[string]any)
	if percent["1-30"] != float64(100) {
		t.Fatalf("single-bucket report must be 100 percent: %v", percent)
	}

	// Applying part of the credit moves it onto the invoice instead of leaving it standalone.
	expectStatus(t, do(t, h, "POST", "/api/v1/customer-credit-notes/"+itoa(int64(credit["id"].(float64)))+"/apply", `{"org_id":`+o+`,"invoice_id":`+itoa(int64(inv["id"].(float64)))+`,"amount_cents":3000,"applied_on":"2026-10-25"}`, owner), 201)
	rep = expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+o+"&as_of=2026-10-29", "", owner), 200)
	if rows := len(rep["rows"].([]any)); rows != 1 {
		t.Fatalf("applied credit must not stay as a separate row, got %d rows", rows)
	}
	if total := rep["totals_by_bucket"].(map[string]any)["1-30"]; total != float64(7000) {
		t.Fatalf("applying the credit must not change the total: %v", total)
	}
}
