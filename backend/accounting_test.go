package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestAccountDescriptionValidation(t *testing.T) {
	in := accountInput{Code: "1200", Name: "Account", Type: "asset", Description: "  Office cash  "}
	if !validAccount(&in) || in.Description != "Office cash" {
		t.Fatal("valid description rejected or not trimmed")
	}
	for _, description := range []string{strings.Repeat("a", 4001), "bad\x00text", string([]byte{0xff})} {
		in.Description = description
		if validAccount(&in) {
			t.Fatal("invalid description accepted")
		}
	}
}

func TestAccountDescriptionPersistence(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "account-description@example.com")
	org := orgFromList(t, a, owner, `{"name":"Account description company","fiscal_year_end":"12-31"}`)
	body := `{"org_id":` + itoa(org) + `,"code":"1250","name":"Cash","type":"asset","description":"  Petty cash  "}`
	created := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", body, owner), 201)
	if created["description"] != "Petty cash" {
		t.Fatal(created)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+itoa(org), "", owner), 200)
	found := false
	for _, entry := range listed["accounts"].([]any) {
		account := entry.(map[string]any)
		if account["code"] == "1250" {
			found = account["description"] == "Petty cash"
		}
	}
	if !found {
		t.Fatal("description not persisted", listed)
	}
}

func TestJournalValidation(t *testing.T) {
	valid := journalInput{EntryDate: "2026-10-29", IdempotencyKey: "test", Lines: []journalLine{{AccountID: 1, DebitCents: 100}, {AccountID: 2, CreditCents: 100}}}
	if !validJournal(&valid) {
		t.Fatal("balanced journal rejected")
	}
	for _, change := range []func(*journalInput){func(v *journalInput) { v.EntryDate = "2026-02-30" }, func(v *journalInput) { v.Lines[0].DebitCents = 101 }, func(v *journalInput) { v.Lines[0].CreditCents = 100 }, func(v *journalInput) { v.Lines[0].AccountID = 0 }, func(v *journalInput) { v.Lines[0].DebitCents = maxMoneyCents + 1 }, func(v *journalInput) { v.IdempotencyKey = "" }} {
		v := valid
		v.Lines = append([]journalLine(nil), valid.Lines...)
		change(&v)
		if validJournal(&v) {
			t.Fatal("invalid journal accepted")
		}
	}
}

func TestManualJournalPostReverseAndIsolation(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "journal-owner@example.com")
	outsider := seedSession(t, a, "journal-outsider@example.com")
	viewer := seedSession(t, a, "journal-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Journal company","fiscal_year_end":"12-31"}`)
	other := orgFromList(t, a, outsider, `{"name":"Other company","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='journal-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	makeAccount := func(token, code, typ string, orgID int64) int64 {
		t.Helper()
		m := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(orgID)+`,"code":"`+code+`","name":"Account `+code+`","type":"`+typ+`"}`, token), 201)
		return int64(m["id"].(float64))
	}
	asset := makeAccount(owner, "1250", "asset", org)
	equity := makeAccount(owner, "3000", "equity", org)
	foreign := makeAccount(outsider, "1250", "asset", other)
	if rec := do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1000","name":"Wrong bank","type":"expense"}`, owner); rec.Code != 409 {
		t.Fatal("reserved account accepted", rec.Code)
	}
	body := `{"org_id":` + itoa(org) + `,"entry_date":"2026-10-29","memo":"Opening balances","idempotency_key":"opening","lines":[{"account_id":` + itoa(asset) + `,"debit_cents":10000},{"account_id":` + itoa(equity) + `,"credit_cents":10000}]}`
	for _, token := range []string{outsider, viewer} {
		if rec := do(t, h, "POST", "/api/v1/journals", body, token); rec.Code != 403 {
			t.Fatal("forbidden write", rec.Code)
		}
	}
	cross := strings.Replace(body, `"account_id":`+itoa(asset), `"account_id":`+itoa(foreign), 1)
	if rec := do(t, h, "POST", "/api/v1/journals", cross, owner); rec.Code != 400 {
		t.Fatal("foreign account accepted", rec.Code, rec.Body)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", "/api/v1/journals", body, owner).Code }()
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code != 200 {
			t.Fatal("concurrent post", code)
		}
	}
	if created != 1 {
		t.Fatal("posted twice", created)
	}
	m := expectStatus(t, do(t, h, "POST", "/api/v1/journals", body, owner), 200)
	id := int64(m["id"].(float64))
	if rec := do(t, h, "POST", "/api/v1/journals", strings.Replace(body, "Opening balances", "Changed", 1), owner); rec.Code != 409 {
		t.Fatal("retry conflict accepted", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/v1/journals/"+itoa(id)+"?org_id="+itoa(org), "", outsider); rec.Code != 403 {
		t.Fatal("journal leaked", rec.Code)
	}
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/journals/"+itoa(id)+"?org_id="+itoa(org), "", viewer), 200)
	if len(detail["lines"].([]any)) != 2 {
		t.Fatal(detail)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/journals?org_id="+itoa(org), "", owner), 200)
	if len(listed["journals"].([]any)) != 1 {
		t.Fatal(listed)
	}
	reverse := `{"org_id":` + itoa(org) + `,"entry_date":"2026-10-30"}`
	path := "/api/v1/journals/" + itoa(id) + "/reverse"
	if rec := do(t, h, "POST", path, reverse, viewer); rec.Code != 403 {
		t.Fatal("viewer reversed", rec.Code)
	}
	first := expectStatus(t, do(t, h, "POST", path, reverse, owner), 201)
	retry := expectStatus(t, do(t, h, "POST", path, reverse, owner), 200)
	if first["id"] != retry["id"] {
		t.Fatal("duplicate reversal", first, retry)
	}
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+itoa(org)+"&as_of=2026-10-31", "", owner), 200)
	if tb["balanced"] != true || tb["total_debit_cents"].(float64) != 0 || tb["total_credit_cents"].(float64) != 0 {
		t.Fatal("reversal did not cancel", tb)
	}
	var audit int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action IN ('journal.posted','journal.reversed')`).Scan(&audit); err != nil || audit != 2 {
		t.Fatal(audit, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE journals SET memo='changed' WHERE id=$1`, id); err == nil {
		t.Fatal("posted entry changed")
	}
	rec := do(t, h, http.MethodGet, "/api/v1/accounts?org_id="+itoa(org), "", owner)
	var accounts map[string]json.RawMessage
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &accounts) != nil {
		t.Fatal(rec.Code, rec.Body)
	}
}
