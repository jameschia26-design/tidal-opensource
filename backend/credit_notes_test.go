package main

import (
	"context"
	"testing"
)

func TestCreditNotesReducePostedInvoiceOnly(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "credit-owner@example.com")
	viewer := seedSession(t, a, "credit-viewer@example.com")
	outsider := seedSession(t, a, "credit-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Credit co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='credit-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Credit customer"}`, owner), 201)
	cid := int64(c["id"].(float64))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-10-01","due_date":"2026-11-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)
	invID := int64(inv["id"].(float64))
	path := "/api/v1/invoices/" + itoa(invID) + "/credit-notes"
	note := `{"org_id":` + itoa(org) + `,"credit_date":"2026-10-15","reason":"Returned work","amount_cents":2500}`

	// Draft invoices cannot be credited.
	expectStatus(t, do(t, h, "POST", path, note, owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+itoa(org), "", owner), 200)

	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "POST", path, note, token), 403)
	}
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"credit_date":"2026-09-30","reason":"Too early","amount_cents":100}`, owner), 422)
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-15","reason":"Too big","amount_cents":10001}`, owner), 422)
	created := expectStatus(t, do(t, h, "POST", path, note, owner), 201)
	if created["amount_cents"] != float64(2500) {
		t.Fatal(created)
	}
	// Remaining credit cap is 7500; a further 7501 is refused, exactly 7500 is accepted.
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-15","reason":"Over","amount_cents":7501}`, owner), 422)
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-15","reason":"Rest","amount_cents":7500}`, owner), 201)
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-15","reason":"Extra","amount_cents":1}`, owner), 422)

	listed := expectStatus(t, do(t, h, "GET", path, "", viewer), 200)["credit_notes"].([]any)
	if len(listed) != 2 {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)

	var sales, ar int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(jl.debit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='4000'`, org).Scan(&sales); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(jl.credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='1100'`, org).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	// Sales was credited by the invoice (10000) and debited by the notes (10000): net zero.
	var salesCredit int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(jl.credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='4000'`, org).Scan(&salesCredit); err != nil {
		t.Fatal(err)
	}
	if sales != 10000 || salesCredit != 10000 || ar != 10000 {
		t.Fatal("ledger does not tie", sales, salesCredit, ar)
	}
	if _, err := pool.Exec(ctx, `UPDATE credit_notes SET amount_cents=1 WHERE org_id=$1`, org); err == nil {
		t.Fatal("credit note mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM credit_notes WHERE org_id=$1`, org); err == nil {
		t.Fatal("credit note deletable")
	}
	// Invoice stays unchanged.
	inv2 := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(invID)+"?org_id="+itoa(org), "", owner), 200)["invoice"].(map[string]any)
	if inv2["total_cents"] != float64(10000) || inv2["status"] != "posted" {
		t.Fatal(inv2)
	}
}
