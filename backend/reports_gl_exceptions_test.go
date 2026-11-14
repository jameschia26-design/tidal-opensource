package main

import (
	"context"
	"testing"
)

func TestGlExceptionsAndAssetRecon(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "exception-owner@example.com")
	outsider := seedSession(t, a, "exception-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Exceptions","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	// Seeded accounts are created on the first write, so create them here and read their IDs from the database.
	code := map[string]string{}
	{
		ctx := context.Background()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := accountIDs(ctx, tx, org)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		for c, id := range ids {
			code[c] = itoa(id)
		}
	}
	makeAccount := func(c, name, typ string) string {
		t.Helper()
		return itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"`+c+`","name":"`+name+`","type":"`+typ+`"}`, owner), 201)["id"].(float64)))
	}
	expense := makeAccount("6500", "Depreciation", "expense")
	// 1500 is the seeded asset cost account the reconciliation compares against.
	var costID int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1500'`, org).Scan(&costID); err != nil {
		t.Fatal(err)
	}
	cost := itoa(costID)
	// A manual journal touching Accounts Receivable is an exception; a plain expense journal is not.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-05","memo":"misdirected","idempotency_key":"exc-1","lines":[{"account_id":`+code[acctAR]+`,"debit_cents":500},{"account_id":`+expense+`,"credit_cents":500}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-06","memo":"normal","idempotency_key":"exc-2","lines":[{"account_id":`+expense+`,"debit_cents":700},{"account_id":`+code[acctBank]+`,"credit_cents":700}]}`, owner), 201)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gl-exceptions?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := got["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(got)
	}
	first := rows[0].(map[string]any)
	if first["rule"] != "control_account" || first["account_code"] != acctAR || first["debit_cents"] != float64(500) {
		t.Fatal(first)
	}

	// Register and ledger disagree because the asset was bought with a manual journal, as the page explains.
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Press","acquired_on":"2026-10-01","cost_cents":100000,"useful_life_months":10}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-01","memo":"buy press","idempotency_key":"exc-3","lines":[{"account_id":`+cost+`,"debit_cents":100000},{"account_id":`+code[acctBank]+`,"credit_cents":100000}]}`, owner), 201)
	rec := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-asset-reconciliation?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	register := rec["register"].(map[string]any)
	ledger := rec["ledger"].(map[string]any)
	difference := rec["difference"].(map[string]any)
	if register["closing_cost_cents"] != float64(100000) || ledger["closing_cost_cents"] != float64(100000) {
		t.Fatal(rec)
	}
	if register["opening_cost_cents"] != float64(0) || difference["closing_cost_cents"] != float64(0) {
		t.Fatal(rec)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/gl-exceptions?org_id="+o+"&from=2026-10-31&to=2026-10-01", "", owner), 400)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/gl-exceptions?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-asset-reconciliation?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
}
