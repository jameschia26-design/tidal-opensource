package main

import "testing"

func TestTaxAndBankrecReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "tax-owner@example.com")
	outsider := seedSession(t, a, "tax-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Tax","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2026-01-01"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[{"description":"work","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
	id := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post", `{"org_id":`+o+`}`, owner), 200)
	// Unposted invoice stays out of the reports.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-05","due_date":"2026-10-05","lines":[{"description":"draft","quantity":1,"unit_price_cents":10000}]}`, owner), 201)
	// Purchases belong in both reports too.
	supplier := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	expense := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Supplies","type":"expense"}`, owner), 201)["id"].(float64)))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supplier+`,"supplier_reference":"TAX-B1","bill_date":"2026-10-06","due_date":"2026-10-06","lines":[{"account_id":`+expense+`,"description":"stock","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
	bid := itoa(int64(bill["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+bid+"/post?org_id="+o, "", owner), 200)

	audit := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-audit?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := audit["rows"].([]any)
	if len(rows) != 2 {
		t.Fatal(audit)
	}
	var billJournal int64
	if err := a.db.QueryRow(t.Context(), `SELECT journal_id FROM bills WHERE org_id=$1 AND id=$2`, org, bill["id"]).Scan(&billJournal); err != nil {
		t.Fatal(err)
	}
	var billDebits, billCredits int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0),COALESCE(sum(credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, org, billJournal).Scan(&billDebits, &billCredits); err != nil {
		t.Fatal(err)
	}
	if billDebits != billCredits || billDebits != 10900 {
		t.Fatalf("bill journal debits=%d credits=%d", billDebits, billCredits)
	}
	kinds := map[string]bool{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		kinds[row["kind"].(string)] = true
		if row["gst_code"] != "SR" || row["gst_cents"] != float64(900) || row["amount_cents"] != float64(10000) {
			t.Fatal(row)
		}
	}
	if !kinds["sale"] || !kinds["purchase"] {
		t.Fatal(audit)
	}
	// Zero-rated, exempt and out-of-scope purchase lines appear with bases but no input tax.
	for i, code := range []string{"ZR", "ES", "OS"} {
		ref := []string{"TAX-ZR", "TAX-ES", "TAX-OS"}[i]
		body := `{"org_id":` + o + `,"contact_id":` + supplier + `,"supplier_reference":"` + ref + `","bill_date":"2026-10-07","due_date":"2026-10-07","lines":[{"account_id":` + expense + `,"description":"` + code + `","quantity":1,"unit_price_cents":5000,"gst_code":"` + code + `"}]}`
		m := expectStatus(t, do(t, h, "POST", "/api/v1/bills", body, owner), 201)
		id := itoa(int64(m["id"].(float64)))
		expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+id+"/post?org_id="+o, "", owner), 200)
	}
	tax := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	trows := tax["rows"].([]any)
	// Rows split by direction and rate: sale 9%, purchase 9%, purchase 0%. Input and output stay separate.
	if len(trows) != 3 || tax["taxable_cents"] != float64(35000) || tax["tax_cents"] != float64(1800) {
		t.Fatal(tax)
	}
	var saleTax, purchaseTax int64
	for _, raw := range trows {
		r := raw.(map[string]any)
		if r["kind"] == "sale" {
			saleTax += int64(r["tax_cents"].(float64))
		} else if r["kind"] == "purchase" {
			purchaseTax += int64(r["tax_cents"].(float64))
		}
	}
	if saleTax != 900 || purchaseTax != 900 {
		t.Fatalf("direction totals sale=%d purchase=%d, want 900/900", saleTax, purchaseTax)
	}
	var reportInputGST, reportOutputGST int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(l.debit_cents-l.credit_cents) FILTER (WHERE a.code='2300' AND l.tax_direction='input'),0),
		COALESCE(sum(l.credit_cents-l.debit_cents) FILTER (WHERE a.code='2300' AND COALESCE(l.tax_direction,'output')='output'),0)
		FROM journals j JOIN journal_lines l ON l.org_id=j.org_id AND l.journal_id=j.id
		JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE j.org_id=$1 AND j.entry_date BETWEEN '2026-10-01' AND '2026-10-31'`, org).Scan(&reportInputGST, &reportOutputGST); err != nil {
		t.Fatal(err)
	}
	if reportInputGST != 900 || reportOutputGST != 900 {
		t.Fatalf("ledger GST input=%d output=%d, expected 900/900", reportInputGST, reportOutputGST)
	}
	kindsSeen := map[string]bool{}
	for _, raw := range trows {
		kindsSeen[raw.(map[string]any)["kind"].(string)] = true
	}
	if !kindsSeen["sale"] || !kindsSeen["purchase"] {
		t.Fatal("sales-tax report omitted a direction", tax)
	}
	// A statement line matched to the invoice payment shows in both buckets.
	pay := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/payments", `{"org_id":`+o+`,"amount_cents":1000,"payment_date":"2026-10-20","idempotency_key":"tax-pay-1"}`, owner), 201)
	pid := int(pay["payment_id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o, "date,description,reference,amount\n2026-10-20,Client pay,,10.00\n", owner), 200)
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o, "", owner), 200)["lines"].([]any)
	lid := int(lines[0].(map[string]any)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(int64(lid))+"/match", `{"org_id":`+o+`,"kind":"invoice","payment_id":`+itoa(int64(pid))+`}`, owner), 200)

	rec := expectStatus(t, do(t, h, "GET", "/api/v1/reports/bank-reconciliation?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	matched := rec["matched"].(map[string]any)
	unmatched := rec["unmatched"].(map[string]any)
	if matched["lines"] != float64(1) || matched["in_cents"] != float64(1000) || unmatched["lines"] != float64(0) {
		t.Fatal(rec)
	}
	if rec["ledger_balance_cents"] != float64(1000) {
		t.Fatal(rec)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-audit?org_id="+o+"&from=2026-10-31&to=2026-10-01", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-audit?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/bank-reconciliation?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
}
