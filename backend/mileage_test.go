package main

import "testing"

func TestMileagePreferencesRatesAndExpense(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "mileage@example.com")
	viewer := seedSession(t, a, "mileage-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Mileage","fiscal_year_end":"12-31"}`)
	other := orgFromList(t, a, viewer, `{"name":"Mileage other","fiscal_year_end":"12-31"}`)
	create := func(code, typ string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"name":"Account `+code+`","code":"`+code+`","type":"`+typ+`"}`, owner), 201)["id"].(float64))
	}
	bank, fuel, asset := create("1010", "asset"), create("6210", "expense"), create("1020", "asset")
	pref := func(account int64, body string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "PUT", "/api/v1/mileage-preferences", `{"org_id":`+itoa(org)+`,"expense_account_id":`+itoa(account)+`,`+body+`}`, owner), status)
	}
	if expectStatus(t, do(t, h, "GET", "/api/v1/mileage-preferences?org_id="+itoa(org), "", owner), 200)["preferences"] != nil {
		t.Fatal("expected no preferences yet")
	}
	// expense with no preferences is refused
	expense := func(key, date, mileage string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+itoa(org)+`,"expense_date":"`+date+`","payee":"Me","description":"Trip","paid_through_account_id":`+itoa(bank)+`,"idempotency_key":"`+key+`","mileage":`+mileage+`}`, owner), status)
	}
	expense("m-none", "2026-10-29", `{"mode":"distance","distance_thousandths":10000}`, 422)
	pref(asset, `"unit":"km","default_rate_cents":50,"rates":[]`, 422)
	pref(fuel, `"unit":"furlong","default_rate_cents":50,"rates":[]`, 400)
	pref(fuel, `"unit":"km","default_rate_cents":50,"rates":[{"start_date":"2026-10-01","rate_cents":70},{"start_date":"2026-10-01","rate_cents":80}]`, 400)
	pref(fuel, `"unit":"km","default_rate_cents":50,"rates":[{"start_date":"2026-10-01","rate_cents":70}]`, 200)
	// other company cannot read or reuse these preferences
	expectStatus(t, do(t, h, "GET", "/api/v1/mileage-preferences?org_id="+itoa(org), "", viewer), 403)
	_ = other

	// before first dated rate: default 50c/km x 12.5km = 625
	if got := expense("m-1", "2026-09-30", `{"mode":"distance","distance_thousandths":12500}`, 201)["amount_cents"]; got != float64(625) {
		t.Fatal(got)
	}
	// after: 70c/km, odometer 1000.000 -> 1010.500 = 10.5km = 735
	created := expense("m-2", "2026-10-29", `{"mode":"odometer","odometer_start_thousandths":1000000,"odometer_end_thousandths":1010500}`, 201)
	if created["amount_cents"] != float64(735) {
		t.Fatal(created)
	}
	if expense("m-2", "2026-10-29", `{"mode":"odometer","odometer_start_thousandths":1000000,"odometer_end_thousandths":1010500}`, 200)["id"] != created["id"] {
		t.Fatal("retry created another expense")
	}
	expense("m-2", "2026-10-29", `{"mode":"distance","distance_thousandths":1000}`, 409)
	expense("m-3", "2026-10-29", `{"mode":"odometer","odometer_start_thousandths":5,"odometer_end_thousandths":5}`, 400)
	expense("m-4", "2026-10-29", `{"mode":"distance","distance_thousandths":0}`, 400)
	expense("m-5", "2026-10-29", `{"mode":"distance","distance_thousandths":1000,"odometer_end_thousandths":3}`, 400)

	for account, want := range map[int64]int64{bank: -1360, fuel: 1360} {
		var got int64
		if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, account).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("account %d got %d want %d", account, got, want)
		}
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+itoa(org), "", owner), 200)["expenses"].([]any)
	m := listed[0].(map[string]any)["mileage"].(map[string]any)
	if len(listed) != 2 || m["unit"] != "km" || m["rate_cents"] != float64(70) || m["distance_thousandths"] != float64(10500) {
		t.Fatal(listed)
	}
}
