package main

import "testing"

func TestLedgerReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ledger-owner@example.com")
	outsider := seedSession(t, a, "ledger-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Ledger","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(v map[string]any) string { return itoa(int64(v["id"].(float64))) }
	bank := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1020","name":"Bank","type":"asset"}`, owner), 201))
	sales := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	post := func(date, key string, cents string) {
		expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"`+date+`","memo":"m","idempotency_key":"`+key+`","lines":[{"account_id":`+bank+`,"debit_cents":`+cents+`},{"account_id":`+sales+`,"credit_cents":`+cents+`}]}`, owner), 201)
	}
	post("2026-09-15", "led-1", "700")
	post("2026-10-10", "led-2", "300")
	post("2026-11-15", "led-future", "900")
	q := "?org_id=" + o + "&from=2026-10-01&to=2026-10-31"
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-lines"+q, "", owner), 200)["lines"].([]any)
	if len(lines) != 2 {
		t.Fatal(lines)
	}
	one := expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-lines"+q+"&account_id="+bank, "", owner), 200)["lines"].([]any)
	if len(one) != 1 || one[0].(map[string]any)["debit_cents"] != float64(300) {
		t.Fatal(one)
	}
	rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-summary"+q, "", owner), 200)["rows"].([]any)
	b := rows[0].(map[string]any)
	if len(rows) != 2 || b["opening_cents"] != float64(700) || b["debit_cents"] != float64(300) || b["closing_cents"] != float64(1000) {
		t.Fatal(rows)
	}
	filtered := expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-summary"+q+"&account_id="+bank, "", owner), 200)["rows"].([]any)
	if len(filtered) != 1 || filtered[0].(map[string]any)["opening_cents"] != float64(700) {
		t.Fatal(filtered)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-summary"+q, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-lines?org_id="+o+"&from=2026-11-01&to=2026-10-01", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/ledger-lines?org_id="+o+"&from=2020-01-01&to=2026-10-01", "", owner), 400)
	// Cash summary reads system Bank account 1000 only; 1020 test account is excluded.
	cash := "/api/v1/reports/cash-summary" + q
	c := expectStatus(t, do(t, h, "GET", cash, "", owner), 200)
	if c["opening_cents"] != float64(0) || c["closing_cents"] != float64(0) {
		t.Fatal(c)
	}
	tx, err := a.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := accountIDs(t.Context(), tx, org)
	if err != nil || tx.Commit(t.Context()) != nil {
		t.Fatal(err)
	}
	sys := itoa(ids["1000"])
	jl := func(date, key, bankSide, otherSide string) {
		expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"`+date+`","memo":"cash","idempotency_key":"`+key+`","lines":[{"account_id":`+sys+`,`+bankSide+`},{"account_id":`+sales+`,`+otherSide+`}]}`, owner), 201)
	}
	jl("2026-09-20", "cash-open", `"debit_cents":500`, `"credit_cents":500`)
	jl("2026-10-05", "cash-in", `"debit_cents":200`, `"credit_cents":200`)
	jl("2026-10-06", "cash-out", `"credit_cents":50`, `"debit_cents":50`)
	jl("2026-11-06", "cash-later", `"debit_cents":900`, `"credit_cents":900`)
	c = expectStatus(t, do(t, h, "GET", cash, "", owner), 200)
	if c["opening_cents"] != float64(500) || c["received_cents"] != float64(200) || c["spent_cents"] != float64(50) || c["closing_cents"] != float64(650) || c["net_cents"] != float64(150) {
		t.Fatal(c)
	}
	expectStatus(t, do(t, h, "GET", cash, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/cash-summary?org_id="+o+"&from=bad&to=2026-10-31", "", owner), 400)
	// Executive summary: October against the 31 days before it (Aug 31 - Sep 30). Income = 300 + 200 cash-in - 50 cash-out journals on Sales.
	ex := expectStatus(t, do(t, h, "GET", "/api/v1/reports/executive-summary"+q, "", owner), 200)
	cur, prev := ex["current"].(map[string]any), ex["previous"].(map[string]any)
	if cur["cash_received_cents"] != float64(200) || cur["cash_spent_cents"] != float64(50) || cur["closing_bank_cents"] != float64(650) || cur["income_cents"] != float64(450) || cur["expenses_cents"] != float64(0) {
		t.Fatal(cur)
	}
	if prev["from"] != "2026-08-31" || prev["to"] != "2026-09-30" || prev["cash_received_cents"] != float64(500) || prev["closing_bank_cents"] != float64(500) || prev["income_cents"] != float64(1200) {
		t.Fatal(prev)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/executive-summary"+q, "", outsider), 403)
}
