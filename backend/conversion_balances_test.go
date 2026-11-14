package main

import "testing"

func TestConversionBalances(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "conv-owner@example.com")
	outsider := seedSession(t, a, "conv-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Conv co","fiscal_year_end":"12-31"}`)
	bank := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1250","name":"Cash","type":"asset"}`, owner), 201)["id"].(float64))
	loan := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"2500","name":"Loan","type":"liability"}`, owner), 201)["id"].(float64))
	body := func(date string, bankDebit, loanCredit int) string {
		return `{"org_id":` + itoa(org) + `,"conversion_date":"` + date + `","lines":[{"account_id":` + itoa(bank) + `,"debit_cents":` + itoa(int64(bankDebit)) + `},{"account_id":` + itoa(loan) + `,"credit_cents":` + itoa(int64(loanCredit)) + `}]}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", body("2026-13-01", 1, 1), owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", `{"org_id":`+itoa(org)+`,"conversion_date":"2026-01-01","lines":[{"account_id":`+itoa(bank)+`,"debit_cents":5,"credit_cents":5}]}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", body("2026-01-01", 100000, 30000), outsider), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", `{"org_id":`+itoa(org)+`,"conversion_date":"2026-01-01","lines":[{"account_id":999999,"debit_cents":5}]}`, owner), 400)
	// Trigger standard account seeding, then verify conversion cannot duplicate contact balances.
	if _, err := pool.Exec(t.Context(), `INSERT INTO accounts(org_id,code,name,type,class) VALUES($1,'1100','Accounts Receivable','asset','current_asset'),($1,'2100','Accounts Payable','liability','current_liability'),($1,'3900','Opening Balance Equity','equity','equity') ON CONFLICT DO NOTHING`, org); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{acctAR, acctAP, acctOBE} {
		var id int64
		if err := pool.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, org, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", `{"org_id":`+itoa(org)+`,"conversion_date":"2026-01-01","lines":[{"account_id":`+itoa(id)+`,"debit_cents":100}]}`, owner), 400)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", body("2026-01-01", 100000, 30000), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", body("2026-01-01", 100000, 30000), owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", body("2026-01-01", 100000, 40000), owner), 409)
	g := expectStatus(t, do(t, h, "GET", "/api/v1/conversion-balances?org_id="+itoa(org), "", owner), 200)
	if g["journal"] == nil {
		t.Fatal(g)
	}
	// Journal balances: the 70,000 difference sits in Opening Balance Equity.
	j := expectStatus(t, do(t, h, "GET", "/api/v1/journals/"+itoa(int64(g["journal"].(map[string]any)["id"].(float64)))+"?org_id="+itoa(org), "", owner), 200)
	var d, c float64
	for _, l := range j["lines"].([]any) {
		m := l.(map[string]any)
		d += m["debit_cents"].(float64)
		c += m["credit_cents"].(float64)
	}
	if len(j["lines"].([]any)) != 3 || d != c || d != 100000 {
		t.Fatal(j)
	}
}

func TestConversionComparativeBalances(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "conv-compare@example.com")
	org := orgFromList(t, a, owner, `{"name":"Conv compare co","fiscal_year_end":"12-31"}`)
	bank := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"1250","name":"Cash","type":"asset"}`, owner), 201)["id"].(float64))
	loan := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"2500","name":"Loan","type":"liability"}`, owner), 201)["id"].(float64))
	grid := func(bankDebit, loanCredit int64) string {
		return `[{"account_id":` + itoa(bank) + `,"debit_cents":` + itoa(bankDebit) + `},{"account_id":` + itoa(loan) + `,"credit_cents":` + itoa(loanCredit) + `}]`
	}
	// Comparative date must be earlier than the conversion date.
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", `{"org_id":`+itoa(org)+`,"conversion_date":"2026-01-01","lines":`+grid(100, 100)+`,"comparative_date":"2026-01-01","comparative_lines":`+grid(50, 50)+`}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", `{"org_id":`+itoa(org)+`,"conversion_date":"2026-01-01","lines":`+grid(1000, 900)+`,"comparative_date":"2025-01-01","comparative_lines":`+grid(600, 500)+`}`, owner), 201)
	// Both journals balance and post their own difference to Opening Balance Equity.
	for _, key := range []string{"conversion-balances", "conversion-comparative-balances"} {
		var id int64
		if err := a.db.QueryRow(t.Context(), `SELECT id FROM journals WHERE org_id=$1 AND idempotency_key=$2`, org, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		var d, c int64
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0),COALESCE(sum(credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, org, id).Scan(&d, &c); err != nil {
			t.Fatal(err)
		}
		if d != c || d == 0 {
			t.Fatalf("key %s unbalanced: %d vs %d", key, d, c)
		}
	}
}
