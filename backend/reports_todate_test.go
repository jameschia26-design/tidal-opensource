package main

import (
	"testing"
)

// TestReportsExcludeFuturePostings proves "to date" means up to today: a forward-dated journal is
// left out of analytics and the P&L unless the caller asks for it with include_future=1.
func TestReportsExcludeFuturePostings(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "to-date@example.com")
	org := orgFromList(t, a, owner, `{"name":"To date","fiscal_year_end":"09-30"}`)
	o := itoa(org)
	bank := accID(t, h, `"1010"`, o, owner, "asset")
	revenue := accID(t, h, `"4100"`, o, owner, "revenue")
	expense := accID(t, h, `"6501"`, o, owner, "expense")

	today, ok := parseDay(reportToday())
	if !ok {
		t.Fatal("reportToday is not a date")
	}
	past := today.AddDate(0, 0, -1).Format("2006-01-02")
	future := today.AddDate(0, 0, 1).Format("2006-01-02")
	from := today.AddDate(0, 0, -30).Format("2006-01-02")
	to := today.AddDate(0, 0, 30).Format("2006-01-02")

	journal := func(date string, amount int64, debit, credit string, key string) {
		t.Helper()
		body := `{"org_id":` + o + `,"entry_date":"` + date + `","memo":"` + key + `","idempotency_key":"` + key + `","lines":[{"account_id":` + debit + `,"debit_cents":` + itoa(amount) + `},{"account_id":` + credit + `,"credit_cents":` + itoa(amount) + `}]}`
		expectStatus(t, do(t, h, "POST", "/api/v1/journals", body, owner), 201)
	}
	journal(past, 10000, bank, revenue, "past-revenue")
	journal(future, 5000, bank, revenue, "future-revenue")
	journal(future, 2000, expense, bank, "future-expense")

	query := "?org_id=" + o + "&from=" + from + "&to=" + to
	defaultAnalytics := expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics"+query, "", owner), 200)
	if defaultAnalytics["to"] != reportToday() {
		t.Fatalf("default analytics period ends at %v, want %s", defaultAnalytics["to"], reportToday())
	}
	totals := defaultAnalytics["totals"].(map[string]any)
	if totals["revenue_cents"].(float64) != 10000 || totals["expense_cents"].(float64) != 0 {
		t.Fatalf("future postings counted by default: %v", totals)
	}
	futureAnalytics := expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics"+query+"&include_future=1", "", owner), 200)
	if futureAnalytics["to"] != to {
		t.Fatalf("include_future kept %v, want %s", futureAnalytics["to"], to)
	}
	totals = futureAnalytics["totals"].(map[string]any)
	if totals["revenue_cents"].(float64) != 15000 || totals["expense_cents"].(float64) != 2000 {
		t.Fatalf("include_future missed the future postings: %v", totals)
	}

	defaultPnL := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl"+query, "", owner), 200)
	if defaultPnL["revenue_cents"].(float64) != 10000 || defaultPnL["expense_cents"].(float64) != 0 {
		t.Fatalf("P&L counted future postings by default: %v", defaultPnL)
	}
	futurePnL := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl"+query+"&include_future=1", "", owner), 200)
	if futurePnL["revenue_cents"].(float64) != 15000 || futurePnL["expense_cents"].(float64) != 2000 {
		t.Fatalf("P&L include_future missed the future postings: %v", futurePnL)
	}

	// A period that starts after today is refused rather than silently reversed by the cap.
	after := today.AddDate(0, 0, 5).Format("2006-01-02")
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+o+"&from="+after+"&to="+to, "", owner), 400)
}
