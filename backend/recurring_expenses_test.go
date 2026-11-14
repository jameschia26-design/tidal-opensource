package main

import (
	"testing"
	"time"
)

func TestNthRunKeepsMonthEnd(t *testing.T) {
	start := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	for k, want := range map[int]string{1: "2026-02-28", 2: "2026-03-31", 3: "2026-04-30"} {
		if got := day(nthRun(start, 1, "month", k)); got != want {
			t.Fatalf("run %d got %s want %s", k, got, want)
		}
	}
	if got := day(nthRun(start, 2, "week", 1)); got != "2026-02-14" {
		t.Fatal(got)
	}
	if got := day(nthRun(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), 1, "year", 1)); got != "2025-02-28" {
		t.Fatal(got)
	}
}

func TestRecurringExpensesRunOnceAndEnd(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recexp@example.com")
	other := seedSession(t, a, "recexp-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Recurring expenses","fiscal_year_end":"12-31"}`)
	create := func(code, typ string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"name":"Account `+code+`","code":"`+code+`","type":"`+typ+`"}`, owner), 201)["id"].(float64))
	}
	bank, rent := create("1010", "asset"), create("6300", "expense")
	body := func(extra string) string {
		return `{"org_id":` + itoa(org) + `,"name":"Office rent","repeat_every":1,"repeat_unit":"month","start_date":"2026-01-31","expense_account_id":` + itoa(rent) + `,"paid_through_account_id":` + itoa(bank) + `,"payee":"Landlord","amount_cents":200000` + extra + `}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-expenses", body(`,"ends_on":"2025-01-01"`), owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-expenses", body(`,"expense_account_id":`+itoa(bank)), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-expenses", body(""), other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-expenses", body(`,"ends_on":"2026-03-31"`), owner), 201)
	run := func(asOf string) []any {
		return expectStatus(t, do(t, h, "POST", "/api/v1/recurring-expenses/run", `{"org_id":`+itoa(org)+`,"as_of":"`+asOf+`"}`, owner), 200)["created_expense_ids"].([]any)
	}
	if n := len(run("2026-02-15")); n != 1 {
		t.Fatalf("first run created %d", n)
	}
	if n := len(run("2026-02-15")); n != 0 {
		t.Fatalf("repeat run created %d", n)
	}
	if n := len(run("2026-12-31")); n != 2 {
		t.Fatalf("catch-up created %d", n)
	}
	if n := len(run("2027-12-31")); n != 0 {
		t.Fatalf("run after end created %d", n)
	}
	var total, count int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0),count(*) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, rent).Scan(&total, &count); err != nil {
		t.Fatal(err)
	}
	if total != 600000 || count != 3 {
		t.Fatal(total, count)
	}
	profiles := expectStatus(t, do(t, h, "GET", "/api/v1/recurring-expenses?org_id="+itoa(org), "", owner), 200)["profiles"].([]any)
	p := profiles[0].(map[string]any)
	if p["active"] != false || p["generated_count"] != float64(3) {
		t.Fatal(p)
	}
	historyPath := "/api/v1/recurring-expenses/" + itoa(int64(p["id"].(float64))) + "/history?org_id=" + itoa(org)
	history := expectStatus(t, do(t, h, "GET", historyPath, "", owner), 200)["expenses"].([]any)
	if len(history) != 3 || history[0].(map[string]any)["date"] != "2026-03-31" || history[0].(map[string]any)["amount_cents"] != float64(200000) {
		t.Fatal(history)
	}
	expectStatus(t, do(t, h, "GET", historyPath, "", other), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/recurring-expenses/999999/history?org_id="+itoa(org), "", owner), 404)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+itoa(org), "", owner), 200)["expenses"].([]any)
	if len(listed) != 3 {
		t.Fatal(len(listed))
	}
}
