package main

import "testing"

func TestControlAccountsFollowPostings(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ctl-owner@example.com")
	other := seedSession(t, a, "ctl-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Control Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)))
	newAR := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1150","name":"Trade debtors","type":"asset"}`, owner), 201))
	newAP := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"2150","name":"Trade creditors","type":"liability"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4500","name":"Fees","type":"revenue"}`, owner), 201))
	_ = rev
	put := func(ar, ap int64, who string, status int) {
		t.Helper()
		expectStatus(t, do(t, h, "PUT", "/api/v1/control-accounts", `{"org_id":`+o+`,"receivable_account_id":`+itoa(ar)+`,"payable_account_id":`+itoa(ap)+`}`, who), status)
	}
	put(newAP, newAP, owner, 422) // payable cannot be the receivable
	put(newAR, newAR, owner, 422)
	put(newAR, newAP, other, 403)
	put(newAR, newAP, owner, 200)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/control-accounts?org_id="+o, "", owner), 200)
	if got["receivable_account_id"] != float64(newAR) || got["payable_account_id"] != float64(newAP) {
		t.Fatal(got)
	}
	inv := id(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-10-29","due_date":"2026-11-28","lines":[{"description":"Work","quantity":1,"unit_price_cents":5000}]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
	var onNew, onOld int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, newAR).Scan(&onNew); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.id=jl.account_id AND a.org_id=jl.org_id WHERE jl.org_id=$1 AND a.code='1100'`, org).Scan(&onOld); err != nil {
		t.Fatal(err)
	}
	if onNew != 5000 || onOld != 0 {
		t.Fatal("invoice did not post to chosen receivable", onNew, onOld)
	}
	var std int64
	if err := pool.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1100'`, org).Scan(&std); err != nil {
		t.Fatal(err)
	}
	put(std, newAP, owner, 409) // chosen receivable still holds a balance
	put(newAR, newAP, owner, 200)
}
