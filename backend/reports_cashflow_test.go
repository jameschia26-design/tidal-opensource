package main

import "testing"

func TestCashflowReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "cashflow-owner@example.com")
	outsider := seedSession(t, a, "cashflow-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Cashflow","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	idOf := func(v map[string]any) string { return itoa(int64(v["id"].(float64))) }
	sales := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201))
	other := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1050","name":"Savings","type":"asset","is_bank_account":true}`, owner), 201))
	equityAcct := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"3000","name":"Share capital","type":"equity"}`, owner), 201))
	tx, err := a.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids, err := accountIDs(t.Context(), tx, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	bank := itoa(ids[acctBank])
	post := func(date, key, acct, debit, credit string) {
		expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"`+date+`","memo":"m","idempotency_key":"`+key+`","lines":[{"account_id":`+acct+`,"debit_cents":`+debit+`,"credit_cents":`+credit+`},{"account_id":`+sales+`,"debit_cents":`+credit+`,"credit_cents":`+debit+`}]}`, owner), 201)
	}
	post("2026-09-10", "cf-open", bank, "900", "0")
	post("2026-10-05", "cf-in", bank, "250", "0")
	post("2026-10-06", "cf-out", bank, "0", "100")
	post("2026-11-01", "cf-later", bank, "500", "0")
	// A bank-to-bank transfer must not count as cash in or out.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-07","memo":"transfer","idempotency_key":"cf-move","lines":[{"account_id":`+other+`,"debit_cents":300},{"account_id":`+bank+`,"credit_cents":300}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-08","memo":"equity","idempotency_key":"cf-eq","lines":[{"account_id":`+bank+`,"debit_cents":40},{"account_id":`+equityAcct+`,"credit_cents":40}]}`, owner), 201)
	q := "/api/v1/reports/bank-summary?org_id=" + o + "&from=2026-10-01&to=2026-10-31"
	sum := expectStatus(t, do(t, h, "GET", q, "", owner), 200)
	rows := sum["rows"].([]any)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	first, second := rows[0].(map[string]any), rows[1].(map[string]any)
	if first["code"] != acctBank || first["opening_cents"] != float64(900) || first["received_cents"] != float64(290) || first["spent_cents"] != float64(400) || first["closing_cents"] != float64(790) {
		t.Fatal(first)
	}
	if second["code"] != "1050" || second["closing_cents"] != float64(300) {
		t.Fatal(second)
	}
	total := sum["total"].(map[string]any)
	if total["opening_cents"] != float64(900) || total["closing_cents"] != float64(1090) {
		t.Fatal(total)
	}
	flows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/cash-flows-direct?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	// Cash is the sum of every bank account, so the bank-to-bank transfer nets out: 900 + 290 - 100.
	if flows["opening_cents"] != float64(900) || flows["in_cents"] != float64(290) || flows["out_cents"] != float64(100) || flows["closing_cents"] != float64(1090) {
		t.Fatal(flows)
	}
	for _, r := range flows["rows"].([]any) {
		if r.(map[string]any)["code"] == "1050" {
			t.Fatal("bank-to-bank transfer counted as cash flow", r)
		}
	}
	eq := expectStatus(t, do(t, h, "GET", "/api/v1/reports/movements-in-equity?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	var found map[string]any
	for _, r := range eq["rows"].([]any) {
		if r.(map[string]any)["code"] == "3000" {
			found = r.(map[string]any)
		}
	}
	if found == nil || found["movement_cents"] != float64(40) || found["closing_cents"] != float64(40) {
		t.Fatal(eq)
	}
	expectStatus(t, do(t, h, "GET", q, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/cash-flows-direct?org_id="+o+"&from=2026-10-31&to=2026-10-01", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/movements-in-equity?org_id="+o+"&from=x&to=2026-10-31", "", owner), 400)
}
