package main

import "testing"

// TestSupplierCreditAndRefund covers R13: a credit linked to a bill reduces the bill balance, the amount
// owed and input GST, appears in the GST report and AP aging, and can be refunded to the bank.
func TestSupplierCreditAndRefund(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "supplier-credit@example.com")
	org := orgFromList(t, a, owner, `{"name":"Supplier credit","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	exp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"SC-1","bill_date":"2026-05-01","due_date":"2026-05-31","lines":[{"account_id":`+itoa(exp)+`,"description":"parts","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
	billID := int64(bill["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/post?org_id="+o, "", owner), 200)

	credit := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-05-10","reason":"Returned parts","net_cents":2000,"gst_cents":180}`, owner), 201)
	if credit["balance_cents"] != float64(8720) || credit["total_cents"] != float64(2180) {
		t.Fatalf("credit response %v", credit)
	}
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(billID)+"?org_id="+o, "", owner), 200)
	if got := detail["bill"].(map[string]any)["balance_cents"]; got != float64(8720) {
		t.Fatalf("bill balance %v, want 8720", got)
	}

	// Input GST: 900 claimed on the bill, 180 reversed by the credit.
	var inputGST int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents-l.credit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.code='2300' AND l.tax_direction='input'`, org).Scan(&inputGST); err != nil {
		t.Fatal(err)
	}
	if inputGST != 720 {
		t.Fatalf("input GST=%d, want 720", inputGST)
	}
	tax := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2026-05-01&to=2026-05-31", "", owner), 200)
	var taxable, taxCents int64
	for _, raw := range tax["rows"].([]any) {
		row := raw.(map[string]any)
		if row["kind"] == "purchase" {
			taxable += int64(row["taxable_cents"].(float64))
			taxCents += int64(row["tax_cents"].(float64))
		}
	}
	if taxable != 8000 || taxCents != 720 {
		t.Fatalf("GST report purchases taxable=%d tax=%d, want 8000/720", taxable, taxCents)
	}
	aging := expectStatus(t, do(t, h, "GET", "/api/v1/reports/payables-aging?org_id="+o+"&as_of=2026-05-31", "", owner), 200)
	var outstanding int64
	for _, raw := range aging["rows"].([]any) {
		outstanding += int64(raw.(map[string]any)["balance_cents"].(float64))
	}
	if outstanding != 8720 {
		t.Fatalf("AP aging=%d, want 8720", outstanding)
	}

	// Over-crediting the bill is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-05-11","reason":"Too much","net_cents":9000,"gst_cents":0}`, owner), 422)

	// Refund received: money in the bank, the credit closed, and no double refund.
	bank := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1030","name":"Bank","type":"asset"}`, owner), 201)["id"].(float64))
	creditID := int64(credit["id"].(float64))
	refund := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(creditID)+"/refund", `{"org_id":`+o+`,"refund_date":"2026-05-20","bank_account_id":`+itoa(bank)+`}`, owner), 200)
	var bankDebit, apDebit int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(debit_cents) FILTER (WHERE account_id=$2),0),
		COALESCE(sum(debit_cents) FILTER (WHERE account_id=$3),0)
		FROM journal_lines WHERE org_id=$1 AND journal_id=$4`, org, bank, 0, int64(refund["journal_id"].(float64))).Scan(&bankDebit, &apDebit); err != nil {
		t.Fatal(err)
	}
	if bankDebit != 2180 {
		t.Fatalf("refund bank debit=%d, want 2180", bankDebit)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(creditID)+"/refund", `{"org_id":`+o+`,"refund_date":"2026-05-21","bank_account_id":`+itoa(bank)+`}`, owner), 409)
}

// TestStandaloneSupplierCreditLines covers the Xero-style line grid: a standalone credit can
// split its net across expense accounts, with GST staying header-level. The old single-account
// path (no lines) is covered by the bill-linked flow above and the importer paths.
func TestStandaloneSupplierCreditLines(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "sc-lines@example.com")
	org := orgFromList(t, a, owner, `{"name":"SC lines","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	parts := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	freight := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6210","name":"Freight","type":"expense"}`, owner), 201)["id"].(float64))
	made := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"credit_date":"2026-06-01","reference":"short delivery","net_cents":3000,"gst_cents":270,"lines":[{"account_id":`+itoa(parts)+`,"description":"parts","net_cents":2000},{"account_id":`+itoa(freight)+`,"description":"freight","net_cents":1000}]}`, owner), 201)
	// Lines that do not foot to the header net are refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"credit_date":"2026-06-01","reference":"bad split","net_cents":3000,"gst_cents":270,"lines":[{"account_id":`+itoa(parts)+`,"description":"parts","net_cents":2500},{"account_id":`+itoa(freight)+`,"description":"freight","net_cents":1000}]}`, owner), 422)
	creditID := int64(made["id"].(float64))
	approved := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(creditID)+"/approve", `{"org_id":`+o+`}`, owner), 200)
	jid := int64(approved["journal_id"].(float64))
	var apDebit, partsCredit, freightCredit, gstCredit int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(debit_cents) FILTER (WHERE account_id=(SELECT id FROM accounts WHERE org_id=$1 AND code='2100')),0),
		COALESCE(sum(credit_cents) FILTER (WHERE account_id=$2),0),
		COALESCE(sum(credit_cents) FILTER (WHERE account_id=$3),0),
		COALESCE(sum(credit_cents) FILTER (WHERE account_id=(SELECT id FROM accounts WHERE org_id=$1 AND code='2300')),0)
		FROM journal_lines WHERE org_id=$1 AND journal_id=$4`, org, parts, freight, jid).Scan(&apDebit, &partsCredit, &freightCredit, &gstCredit); err != nil {
		t.Fatal(err)
	}
	if apDebit != 3270 || partsCredit != 2000 || freightCredit != 1000 || gstCredit != 270 {
		t.Fatalf("ap=%d parts=%d freight=%d gst=%d, want 3270/2000/1000/270", apDebit, partsCredit, freightCredit, gstCredit)
	}
}
