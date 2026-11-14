package main

import "testing"

// TestExpenseGST covers R14: a GST-registered company records claimable input tax on a paid expense, a
// non-registered company cannot, and blocked categories such as private use cannot claim.
func TestExpenseGST(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "expense-gst@example.com")
	plain := seedSession(t, a, "expense-gst-plain@example.com")
	org := orgFromList(t, a, owner, `{"name":"Expense GST","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	plainOrg := orgFromList(t, a, plain, `{"name":"Expense GST plain","fiscal_year_end":"12-31"}`)
	mk := func(orgID int64, token, code, name, typ string) int64 {
		t.Helper()
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(orgID)+`,"code":"`+code+`","name":"`+name+`","type":"`+typ+`"}`, token), 201)["id"].(float64))
	}
	exp := mk(org, owner, "6200", "Supplies", "expense")
	bank := mk(org, owner, "1020", "Bank", "asset")
	plainExp := mk(plainOrg, plain, "6200", "Supplies", "expense")
	plainBank := mk(plainOrg, plain, "1020", "Bank", "asset")

	body := func(orgID, account, pay int64, code string, blocked bool) string {
		extra := ""
		if blocked {
			extra = `,"blocked_input":true`
		}
		return `{"org_id":` + itoa(orgID) + `,"expense_date":"2026-09-10","payee":"Shop","description":"Paper","expense_account_id":` + itoa(account) + `,"amount_cents":10900,"paid_through_account_id":` + itoa(pay) + `,"gst_code":"` + code + `"` + extra + `}`
	}
	created := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body(org, exp, bank, "SR", false), owner), 201)
	var debitExp, debitGST, creditBank int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.code='6200'),0),
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.code='2300' AND l.tax_direction='input'),0),
		COALESCE(sum(l.credit_cents) FILTER (WHERE a.code='1020'),0)
		FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE l.org_id=$1 AND l.journal_id=$2`, org, int64(created["journal_id"].(float64))).Scan(&debitExp, &debitGST, &creditBank); err != nil {
		t.Fatal(err)
	}
	if debitExp != 10000 || debitGST != 900 || creditBank != 10900 {
		t.Fatalf("expense legs net=%d gst=%d bank=%d, want 10000/900/10900", debitExp, debitGST, creditBank)
	}
	tax := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+itoa(org)+"&from=2026-09-01&to=2026-09-30", "", owner), 200)
	var taxable, taxCents int64
	for _, raw := range tax["rows"].([]any) {
		row := raw.(map[string]any)
		if row["kind"] == "purchase" {
			taxable += int64(row["taxable_cents"].(float64))
			taxCents += int64(row["tax_cents"].(float64))
		}
	}
	if taxable != 10000 || taxCents != 900 {
		t.Fatalf("GST report purchases taxable=%d tax=%d, want 10000/900", taxable, taxCents)
	}

	// Not registered: SR is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body(plainOrg, plainExp, plainBank, "SR", false), plain), 422)
	// Registered but blocked (private use): refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body(org, exp, bank, "SR", true), owner), 422)
	// A plain expense with no GST still records exactly as before.
	if m := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body(org, exp, bank, "NONE", false), owner), 201); m["amount_cents"] != float64(10900) {
		t.Fatalf("no-GST expense %v", m)
	}
}
