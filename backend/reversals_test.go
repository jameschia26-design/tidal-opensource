package main

import "testing"

// TestDocumentReversals covers R15: posted bills, bill payments and expenses are reversed by a mirrored
// journal, balances and bank/expense ledgers come back to where they were, a bank-matched payment cannot be
// reversed until it is unmatched, and a reversal inside a closed period moves to the first open day.
func TestDocumentReversals(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "reversals@example.com")
	org := orgFromList(t, a, owner, `{"name":"Reversals","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	exp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	// Seeded accounts exist after the first write.
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-04-01","payee":"Seed","description":"Seed","expense_account_id":`+itoa(exp)+`,"amount_cents":100}`, owner), 201)

	balance := func(id int64) int64 {
		t.Helper()
		got := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(id)+"?org_id="+o, "", owner), 200)
		return int64(got["bill"].(map[string]any)["balance_cents"].(float64))
	}
	ledger := func(code string) int64 {
		t.Helper()
		var net int64
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents-l.credit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.code=$2`, org, code).Scan(&net); err != nil {
			t.Fatal(err)
		}
		return net
	}

	// Bill 10,000: pay 4,000, then reverse the payment.
	bill := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"RV-1","bill_date":"2026-04-05","due_date":"2026-04-30","lines":[{"account_id":`+itoa(exp)+`,"description":"parts","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/post?org_id="+o, "", owner), 200)
	bankBefore := ledger("1000")
	pay := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/payments", `{"org_id":`+o+`,"amount_cents":4000,"payment_date":"2026-04-10","idempotency_key":"rv-pay-1"}`, owner), 201)
	payID := int64(pay["payment_id"].(float64))
	if balance(bill) != 6000 {
		t.Fatalf("after payment balance=%d, want 6000", balance(bill))
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/"+itoa(payID)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-11"}`, owner), 200)
	if balance(bill) != 10000 {
		t.Fatalf("after reversing payment balance=%d, want 10000", balance(bill))
	}
	if got := ledger("1000"); got != bankBefore {
		t.Fatalf("bank after reversing payment=%d, want %d", got, bankBefore)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/"+itoa(payID)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-11"}`, owner), 409)

	// A bill with a live payment cannot be reversed.
	pay3 := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/payments", `{"org_id":`+o+`,"amount_cents":500,"payment_date":"2026-04-12","idempotency_key":"rv-pay-3"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-12"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/"+itoa(int64(pay3["payment_id"].(float64)))+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-12"}`, owner), 200)

	// Expense 5,000: reverse restores both the bank and the expense account.
	beforeExpense := ledger("6200")
	beforeBank := ledger("1000")
	created := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-04-15","payee":"Shop","description":"Paper","expense_account_id":`+itoa(exp)+`,"amount_cents":5000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses/"+itoa(int64(created["id"].(float64)))+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-16"}`, owner), 200)
	if got := ledger("6200"); got != beforeExpense {
		t.Fatalf("expense account=%d, want %d", got, beforeExpense)
	}
	if got := ledger("1000"); got != beforeBank {
		t.Fatalf("bank=%d, want %d", got, beforeBank)
	}

	// A payment matched to a bank line must be unmatched before it can be reversed.
	pay2 := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/payments", `{"org_id":`+o+`,"amount_cents":1000,"payment_date":"2026-04-20","idempotency_key":"rv-pay-2"}`, owner), 201)
	pay2ID := int64(pay2["payment_id"].(float64))
	csvBody := "Date,Description,Reference,Amount\n2026-04-20,Card payment,RV-MATCH,-10.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o, csvBody, owner), 200)
	var lineID int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM bank_statement_lines WHERE org_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/match", `{"org_id":`+o+`,"kind":"bill","payment_id":`+itoa(pay2ID)+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/"+itoa(pay2ID)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-21"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/unmatch", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/"+itoa(pay2ID)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-04-21"}`, owner), 200)
	var audit int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action IN ('bank.unmatched','bill_payment.reversed')`, org).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if audit != 4 {
		t.Fatalf("audit events=%d, want 4", audit)
	}

	// Reversal requested inside a closed period lands on the first open day.
	exp2 := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-05-10","payee":"Shop","description":"May","expense_account_id":`+itoa(exp)+`,"amount_cents":2000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2026-05-31"}`, owner), 200)
	rev := expectStatus(t, do(t, h, "POST", "/api/v1/expenses/"+itoa(int64(exp2["id"].(float64)))+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-05-20"}`, owner), 200)
	if rev["reversal_date"] != "2026-06-01" {
		t.Fatalf("reversal date %v, want 2026-06-01", rev["reversal_date"])
	}

	// Bill with no payments reverses cleanly.
	bill2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"RV-2","bill_date":"2026-06-05","due_date":"2026-06-30","lines":[{"account_id":`+itoa(exp)+`,"description":"parts","quantity":1,"unit_price_cents":3000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill2)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill2)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-06-06"}`, owner), 200)
	if got := balance(bill2); got != 0 {
		t.Fatalf("reversed bill balance=%d, want 0", got)
	}
	aging := expectStatus(t, do(t, h, "GET", "/api/v1/reports/payables-aging?org_id="+o+"&as_of=2026-12-31", "", owner), 200)
	var outstanding int64
	for _, raw := range aging["rows"].([]any) {
		row := raw.(map[string]any)
		if row["document"] == "RV-2" {
			t.Fatalf("reversed bill still in aging: %v", row)
		}
		outstanding += int64(row["balance_cents"].(float64))
	}
	if outstanding != 10000 {
		t.Fatalf("aging outstanding=%d, want 10000", outstanding)
	}
}
