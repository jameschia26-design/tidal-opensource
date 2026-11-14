package main

import "testing"

// TestBankReconciliationTwoAccounts covers R16: statements belong to a chosen account, every cash type can be
// matched (expenses, receipts, transfers, fees and one grouped deposit), the statement and the ledger agree
// to the cent, the date tolerance is configurable, and unmatching is audited and refused in a closed period.
func TestBankReconciliationTwoAccounts(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-recon@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank recon","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	expAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	bank2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1010","name":"Bank 2","type":"asset","is_bank_account":true}`, owner), 201)["id"].(float64))
	// The first expense seeds the chart, including the default bank account (1000).
	expense := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-07-05","payee":"Shop","description":"Parts","expense_account_id":`+itoa(expAcct)+`,"amount_cents":100000,"paid_through_account_id":`+itoa(bank2)+`}`, owner), 201)
	fee := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-07-08","payee":"Bank","description":"Fee","expense_account_id":`+itoa(expAcct)+`,"amount_cents":5000,"paid_through_account_id":`+itoa(bank2)+`}`, owner), 201)
	var bank1 int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1000'`, org).Scan(&bank1); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1010'`, org).Scan(&bank2); err != nil {
		t.Fatal(err)
	}
	var salesAcct int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='4000'`, org).Scan(&salesAcct); err != nil {
		t.Fatal(err)
	}
	customer := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	receipt := expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", `{"org_id":`+o+`,"contact_id":`+itoa(customer)+`,"receipt_number":"SR-1","receipt_date":"2026-07-06","payment_mode":"bank_transfer","deposit_account_id":`+itoa(bank2)+`,"lines":[{"description":"Sale","account_id":`+itoa(salesAcct)+`,"quantity":1,"unit_price_cents":200000}],"idempotency_key":"recon-receipt-1"}`, owner), 201)
	receiptID := int64(receipt["id"].(float64))
	transfer := expectStatus(t, do(t, h, "POST", "/api/v1/transfers", `{"org_id":`+o+`,"from_account_id":`+itoa(bank2)+`,"to_account_id":`+itoa(bank1)+`,"entry_date":"2026-07-07","amount_cents":50000,"idempotency_key":"recon-transfer-1"}`, owner), 201)
	transferID := int64(transfer["id"].(float64))

	// One grouped deposit covers three invoice payments on the default bank account.
	grouped := "Date,Description,Reference,Amount\n2026-07-07,Transfer in,TR-1,500.00\n2026-07-10,Grouped deposit,GRP,3000.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank1), grouped, owner), 200)
	var paymentIDs []int64
	for i := 1; i <= 3; i++ {
		inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(customer)+`,"reference":"INV-`+itoa(int64(i))+`","invoice_date":"2026-07-01","due_date":"2026-07-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":100000,"revenue_account_id":`+itoa(salesAcct)+`}]}`, owner), 201)["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
		pay := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/payments", `{"org_id":`+o+`,"amount_cents":100000,"payment_date":"2026-07-10","idempotency_key":"recon-inv-`+itoa(int64(i))+`"}`, owner), 201)
		paymentIDs = append(paymentIDs, int64(pay["payment_id"].(float64)))
	}

	// Bank 2 statement: expense, receipt, transfer out and fee.
	stmt := "Date,Description,Reference,Amount\n2026-07-05,Parts,EXP-1,-1000.00\n2026-07-06,Sale,SR-1,2000.00\n2026-07-07,Transfer out,TR-1,-500.00\n2026-07-08,Bank fee,FEE-1,-50.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank2), stmt, owner), 200)
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank2), "", owner), 200)["lines"].([]any)
	lineID := func(ref string) int64 {
		for _, raw := range lines {
			row := raw.(map[string]any)
			if row["reference"] == ref {
				return int64(row["id"].(float64))
			}
		}
		t.Fatalf("line %s not found", ref)
		return 0
	}

	// The transfer is dated one day before its statement line: allowed once the tolerance is set.
	expectStatus(t, do(t, h, "PUT", "/api/v1/organizations/"+o, `{"name":"Bank recon","fiscal_year_end":"12-31","bank_match_tolerance_days":2}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("EXP-1"))+"/match", `{"org_id":`+o+`,"kind":"expense","payment_id":`+itoa(int64(expense["id"].(float64)))+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("SR-1"))+"/match", `{"org_id":`+o+`,"kind":"receipt","payment_id":`+itoa(receiptID)+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("TR-1"))+"/match", `{"org_id":`+o+`,"kind":"transfer","payment_id":`+itoa(transferID)+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("FEE-1"))+"/match", `{"org_id":`+o+`,"kind":"expense","payment_id":`+itoa(int64(fee["id"].(float64)))+`}`, owner), 200)

	lines1 := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank1), "", owner), 200)["lines"].([]any)
	var groupedID, transferInID int64
	for _, raw := range lines1 {
		row := raw.(map[string]any)
		switch row["reference"] {
		case "GRP":
			groupedID = int64(row["id"].(float64))
		case "TR-1":
			transferInID = int64(row["id"].(float64))
		}
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(transferInID)+"/match", `{"org_id":`+o+`,"kind":"transfer","payment_id":`+itoa(transferID)+`}`, owner), 200)
	for _, pid := range paymentIDs {
		expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(groupedID)+"/match", `{"org_id":`+o+`,"kind":"invoice","payment_id":`+itoa(pid)+`}`, owner), 200)
	}
	// A fourth payment of the same amount would overfill the line.
	extra := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(customer)+`,"reference":"INV-9","invoice_date":"2026-07-01","due_date":"2026-07-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":100000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(extra)+"/post?org_id="+o, "", owner), 200)
	extraPay := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(extra)+"/payments", `{"org_id":`+o+`,"amount_cents":100000,"payment_date":"2026-08-05","idempotency_key":"recon-inv-9"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(groupedID)+"/match", `{"org_id":`+o+`,"kind":"invoice","payment_id":`+itoa(int64(extraPay["payment_id"].(float64)))+`}`, owner), 422)

	recon := func(bank int64) map[string]any {
		t.Helper()
		got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/bank-reconciliation?org_id="+o+"&from=2026-07-01&to=2026-07-31&bank_account_id="+itoa(bank), "", owner), 200)
		if got["unmatched"].(map[string]any)["lines"] != float64(0) {
			t.Fatalf("unmatched lines remain on %d: %v", bank, got["unmatched"])
		}
		if got["difference_cents"] != float64(0) {
			t.Fatalf("recon on account %d: %v", bank, got)
		}
		return got
	}
	if got := recon(bank2); got["statement_closing_cents"] != float64(45000) || got["matched_documents"] != float64(4) {
		t.Fatalf("bank 2 recon %v", got)
	}
	if got := recon(bank1); got["statement_closing_cents"] != float64(350000) || got["matched_documents"] != float64(4) {
		t.Fatalf("bank 1 recon %v", got)
	}

	// A 5-day gap is beyond the configured tolerance.
	expectStatus(t, do(t, h, "PUT", "/api/v1/organizations/"+o, `{"name":"Bank recon","fiscal_year_end":"12-31","bank_match_tolerance_days":2}`, owner), 200)
	far := "Date,Description,Reference,Amount\n2026-07-12,Late fee,FEE-2,-50.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank2), far, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("FEE-1"))+"/unmatch", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("FEE-1"))+"/match", `{"org_id":`+o+`,"kind":"expense","payment_id":`+itoa(int64(fee["id"].(float64)))+`}`, owner), 200)

	// Unmatching is audited, and refused once the line's period is closed.
	var audited int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='bank.unmatched'`, org).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("unmatch audits=%d, want 1", audited)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2026-07-31"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID("FEE-1"))+"/unmatch", `{"org_id":`+o+`}`, owner), 422)
}
