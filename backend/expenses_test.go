package main

import (
	"context"
	"strings"
	"testing"
)

func TestPaidExpensesPostAndProtect(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "expense-owner@example.com")
	viewer := seedSession(t, a, "expense-viewer@example.com")
	outsider := seedSession(t, a, "expense-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Expense co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='expense-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	// Create a bank and an expense account via the existing account endpoint so seeding happens normally.
	expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+itoa(org), "", owner), 200)
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Office supplies","type":"expense"}`, owner), 201)
	exp := int64(acct["id"].(float64))
	input := `{"org_id":` + itoa(org) + `,"expense_date":"2026-10-29","payee":"Stationery Shop","description":"Printer paper","expense_account_id":` + itoa(exp) + `,"amount_cents":4550,"receipt_reference":"R-1001","notes":"Paid by card"}`
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "POST", "/api/v1/expenses", input, token), 403)
	}
	for _, bad := range []string{
		strings.Replace(input, `"amount_cents":4550`, `"amount_cents":0`, 1),
		strings.Replace(input, `"payee":"Stationery Shop"`, `"payee":"  "`, 1),
		strings.Replace(input, `"expense_account_id":`+itoa(exp), `"expense_account_id":999999`, 1),
		strings.Replace(input, `"receipt_reference":"R-1001"`, `"receipt_reference":"bad\u0000ref"`, 1),
	} {
		rec := do(t, h, "POST", "/api/v1/expenses", bad, owner)
		if rec.Code != 400 && rec.Code != 422 {
			t.Fatal("expected refusal", rec.Code, rec.Body.String())
		}
	}
	// Using a non-expense account is refused.
	bank := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6200","name":"Other income","type":"revenue"}`, owner), 201)
	wrongType := strings.Replace(input, `"expense_account_id":`+itoa(exp), `"expense_account_id":`+itoa(int64(bank["id"].(float64))), 1)
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", wrongType, owner), 422)

	created := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", input, owner), 201)
	if created["amount_cents"] != float64(4550) {
		t.Fatal(created)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", input, owner), 200)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+itoa(org), "", viewer), 200)["expenses"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["receipt_reference"] != "R-1001" {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+itoa(org), "", outsider), 403)

	var debit, credit int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(jl.debit_cents),0), COALESCE(sum(jl.credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='6100'`, org).Scan(&debit, &credit); err != nil {
		t.Fatal(err)
	}
	if debit != 4550 || credit != 0 {
		t.Fatal("expense debit not posted", debit, credit)
	}
	var bankCredit int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(jl.credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='1000'`, org).Scan(&bankCredit); err != nil {
		t.Fatal(err)
	}
	if bankCredit != 4550 {
		t.Fatal("bank not credited", bankCredit)
	}
	if _, err := pool.Exec(ctx, `UPDATE expenses SET amount_cents=1 WHERE org_id=$1`, org); err == nil {
		t.Fatal("posted expense mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM expenses WHERE org_id=$1`, org); err == nil {
		t.Fatal("posted expense deletable")
	}

	// A custom field value saved with the expense is stored in the same transaction.
	field := expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+itoa(org)+`,"entity_kind":"expenses","label":"Project code","field_type":"text"}`, owner), 201)
	withField := `{"org_id":` + itoa(org) + `,"expense_date":"2026-10-29","payee":"Stationery Shop","description":"Printer paper","expense_account_id":` + itoa(exp) + `,"amount_cents":4550,"idempotency_key":"exp-custom-01","custom":{"` + itoa(int64(field["id"].(float64))) + `":"P-77"}}`
	saved := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", withField, owner), 201)
	var stored string
	if err := pool.QueryRow(ctx, `SELECT custom::text FROM entity_extras WHERE org_id=$1 AND entity_kind='expenses' AND entity_id=$2`, org, int64(saved["id"].(float64))).Scan(&stored); err != nil || !strings.Contains(stored, "P-77") {
		t.Fatal(stored, err)
	}
}
