package main

import (
	"context"
	"testing"
)

func TestTransferValidation(t *testing.T) {
	ok := transferInput{FromAccountID: 1, ToAccountID: 2, EntryDate: "2026-10-30", AmountCents: 100, IdempotencyKey: "k"}
	if !validTransfer(&ok) {
		t.Fatal("valid transfer rejected")
	}
	for name, change := range map[string]func(*transferInput){
		"same account": func(v *transferInput) { v.ToAccountID = 1 },
		"zero amount":  func(v *transferInput) { v.AmountCents = 0 },
		"negative":     func(v *transferInput) { v.AmountCents = -1 },
		"too large":    func(v *transferInput) { v.AmountCents = maxMoneyCents + 1 },
		"bad date":     func(v *transferInput) { v.EntryDate = "2026-02-30" },
		"no key":       func(v *transferInput) { v.IdempotencyKey = "" },
	} {
		v := ok
		change(&v)
		if validTransfer(&v) {
			t.Fatal(name, "accepted")
		}
	}
}

func TestTransferPostsOnceAndIsolates(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "transfer-owner@example.com")
	outsider := seedSession(t, a, "transfer-outsider@example.com")
	viewer := seedSession(t, a, "transfer-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Transfer co","fiscal_year_end":"12-31"}`)
	other := orgFromList(t, a, outsider, `{"name":"Other co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='transfer-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	mk := func(token, code string, orgID int64) int64 {
		m := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(orgID)+`,"code":"`+code+`","name":"Acct `+code+`","type":"asset"}`, token), 201)
		return int64(m["id"].(float64))
	}
	from, to, foreign := mk(owner, "1250", org), mk(owner, "1210", org), mk(outsider, "1250", other)
	body := `{"org_id":` + itoa(org) + `,"from_account_id":` + itoa(from) + `,"to_account_id":` + itoa(to) + `,"entry_date":"2026-10-30","amount_cents":2500,"reference":"Float","idempotency_key":"t1"}`
	first := expectStatus(t, do(t, h, "POST", "/api/v1/transfers", body, owner), 201)
	retry := expectStatus(t, do(t, h, "POST", "/api/v1/transfers", body, owner), 200)
	if first["id"] != retry["id"] {
		t.Fatal("duplicate transfer", first, retry)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/transfers", `{"org_id":`+itoa(org)+`,"from_account_id":`+itoa(from)+`,"to_account_id":`+itoa(to)+`,"entry_date":"2026-10-30","amount_cents":9999,"reference":"Float","idempotency_key":"t1"}`, owner), 409)
	if rec := do(t, h, "POST", "/api/v1/transfers", body, viewer); rec.Code != 403 {
		t.Fatal("viewer posted", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/transfers", body, outsider); rec.Code != 403 {
		t.Fatal("outsider posted", rec.Code)
	}
	cross := `{"org_id":` + itoa(org) + `,"from_account_id":` + itoa(foreign) + `,"to_account_id":` + itoa(to) + `,"entry_date":"2026-10-30","amount_cents":100,"idempotency_key":"t2"}`
	if rec := do(t, h, "POST", "/api/v1/transfers", cross, owner); rec.Code != 400 {
		t.Fatal("foreign account accepted", rec.Code)
	}
	exp := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6199","name":"Expense","type":"expense"}`, owner), 201)
	nonAsset := `{"org_id":` + itoa(org) + `,"from_account_id":` + itoa(from) + `,"to_account_id":` + itoa(int64(exp["id"].(float64))) + `,"entry_date":"2026-10-30","amount_cents":100,"idempotency_key":"t3"}`
	if rec := do(t, h, "POST", "/api/v1/transfers", nonAsset, owner); rec.Code != 400 {
		t.Fatal("non-asset account accepted", rec.Code)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/transfers?org_id="+itoa(org), "", viewer), 200)
	rows := listed["transfers"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["amount_cents"].(float64) != 2500 {
		t.Fatal(listed)
	}
	if rec := do(t, h, "GET", "/api/v1/transfers?org_id="+itoa(org), "", outsider); rec.Code != 403 {
		t.Fatal("list leaked", rec.Code)
	}
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+itoa(org)+"&as_of=2026-10-31", "", owner), 200)
	if tb["balanced"] != true || tb["total_debit_cents"].(float64) != 2500 {
		t.Fatal(tb)
	}
}
