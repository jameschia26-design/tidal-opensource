package main

import (
	"net/http"
	"testing"
)

// shapeFixture builds a chart with one account in every Xero class and one balanced journal that
// moves each of them, so the statements can be checked against hand-computed figures.
// Debits 194000: bank 100000, current asset 10000, fixed 20000, inventory 5000, prepayment 2000,
// non-current asset 3000, cost of sales 30000, expense 20000, other expense 4000.
// Credits 194000: sales 127000, revenue 30000, other income 10000, current liability 9000,
// non-current liability 6000, equity 7000, retained earnings 5000.
func shapeFixture(t *testing.T, email string) (http.Handler, string, string, map[string]string) {
	t.Helper()
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, email)
	org := itoa(int64(orgFromList(t, a, owner, `{"name":"Shape Pte","fiscal_year_end":"12-31"}`)))
	ids := map[string]string{}
	for _, c := range []struct{ code, name, typ, class string }{
		{"2810", "Savings", "asset", "bank"},
		{"1210", "Receivables", "asset", "current_asset"},
		{"1710", "Plant", "asset", "fixed_asset"},
		{"2410", "Stock on hand", "asset", "inventory"},
		{"1410", "Prepaid rent", "asset", "prepayment"},
		{"1610", "Goodwill", "asset", "non_current_asset"},
		{"2210", "Trade payables", "liability", "current_liability"},
		{"2510", "Term loan", "liability", "non_current_liability"},
		{"3960", "Share capital", "equity", "equity"},
		{"3961", "Retained earnings", "equity", "retained_earnings"},
		{"4110", "Sales", "revenue", "sales"},
		{"8110", "Service revenue", "revenue", "revenue"},
		{"8210", "Interest income", "revenue", "other_income"},
		{"5210", "Freight", "expense", "cost_of_sales"},
		{"6510", "Rent", "expense", "expense"},
		{"6910", "Donations", "expense", "other_expense"},
	} {
		created := expectStatus(t, do(t, h, "POST", "/api/v1/accounts",
			`{"org_id":`+org+`,"code":"`+c.code+`","name":"`+c.name+`","type":"`+c.typ+`","class":"`+c.class+`"}`, owner), 201)
		ids[c.class] = itoa(int64(created["id"].(float64)))
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+org+`,"entry_date":"2026-01-15","memo":"Shape ledger","idempotency_key":"shape-1","lines":[
		{"account_id":`+ids["bank"]+`,"debit_cents":100000},
		{"account_id":`+ids["current_asset"]+`,"debit_cents":10000},
		{"account_id":`+ids["fixed_asset"]+`,"debit_cents":20000},
		{"account_id":`+ids["inventory"]+`,"debit_cents":5000},
		{"account_id":`+ids["prepayment"]+`,"debit_cents":2000},
		{"account_id":`+ids["non_current_asset"]+`,"debit_cents":3000},
		{"account_id":`+ids["cost_of_sales"]+`,"debit_cents":30000},
		{"account_id":`+ids["expense"]+`,"debit_cents":20000},
		{"account_id":`+ids["other_expense"]+`,"debit_cents":4000},
		{"account_id":`+ids["sales"]+`,"credit_cents":127000},
		{"account_id":`+ids["revenue"]+`,"credit_cents":30000},
		{"account_id":`+ids["other_income"]+`,"credit_cents":10000},
		{"account_id":`+ids["current_liability"]+`,"credit_cents":9000},
		{"account_id":`+ids["non_current_liability"]+`,"credit_cents":6000},
		{"account_id":`+ids["equity"]+`,"credit_cents":7000},
		{"account_id":`+ids["retained_earnings"]+`,"credit_cents":5000}]}`, owner), 201)
	return h, owner, org, ids
}

func cents(t *testing.T, body map[string]any, key string) int64 {
	t.Helper()
	v, ok := body[key].(float64)
	if !ok {
		t.Fatalf("%s missing from %v", key, body)
	}
	return int64(v)
}

func sectionsByKey(t *testing.T, body map[string]any, field string) map[string]map[string]any {
	t.Helper()
	raw, ok := body[field].([]any)
	if !ok {
		t.Fatalf("%s missing from %v", field, body)
	}
	out := map[string]map[string]any{}
	for _, entry := range raw {
		section := entry.(map[string]any)
		out[section["key"].(string)] = section
	}
	return out
}

func TestProfitAndLossXeroShape(t *testing.T) {
	h, owner, org, _ := shapeFixture(t, "shape-pnl@example.com")
	pl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+org+"&from=2026-01-01&to=2026-12-31", "", owner), 200)

	for _, want := range []struct {
		key  string
		want int64
	}{
		{"revenue_cents", 157000},
		{"cost_of_sales_cents", 30000},
		{"gross_profit_cents", 127000},
		{"operating_expenses_cents", 20000},
		{"operating_profit_cents", 107000},
		{"other_income_cents", 10000},
		{"other_expense_cents", 4000},
		{"expense_cents", 50000},
		{"profit_cents", 113000},
	} {
		if got := cents(t, pl, want.key); got != want.want {
			t.Errorf("%s = %d, want %d", want.key, got, want.want)
		}
	}
	sections := sectionsByKey(t, pl, "sections")
	if len(sections) != 5 {
		t.Fatalf("want 5 P&L sections, got %d: %v", len(sections), sections)
	}
	for key, want := range map[string]int64{"revenue": 157000, "cost_of_sales": 30000, "operating_expenses": 20000, "other_income": 10000, "other_expense": 4000} {
		if got := cents(t, sections[key], "total_cents"); got != want {
			t.Errorf("section %s total = %d, want %d", key, got, want)
		}
	}
	// Sales and revenue share the revenue section; other income does not leak into it.
	revLines := sections["revenue"]["lines"].([]any)
	if len(revLines) != 2 {
		t.Fatalf("revenue lines = %v", revLines)
	}
	classes := map[string]string{}
	for _, entry := range revLines {
		line := entry.(map[string]any)
		classes[line["code"].(string)] = line["class"].(string)
	}
	if classes["4110"] != "sales" || classes["8110"] != "revenue" {
		t.Fatalf("revenue lines carry the wrong classes: %v", classes)
	}
	// The flat lines list stays for older callers and holds every P&L account once.
	if got := len(pl["lines"].([]any)); got != 6 {
		t.Fatalf("flat lines = %d, want 6", got)
	}
}

func TestBalanceSheetXeroShape(t *testing.T) {
	h, owner, org, _ := shapeFixture(t, "shape-bs@example.com")
	bs := expectStatus(t, do(t, h, "GET", "/api/v1/reports/balance-sheet?org_id="+org+"&as_of=2026-12-31", "", owner), 200)

	for _, want := range []struct {
		key  string
		want int64
	}{
		{"assets_cents", 140000},
		{"liabilities_cents", 15000},
		{"equity_cents", 125000},
		{"net_assets_cents", 125000},
	} {
		if got := cents(t, bs, want.key); got != want.want {
			t.Errorf("%s = %d, want %d", want.key, got, want.want)
		}
	}
	if bs["balanced"] != true {
		t.Fatalf("balance sheet does not balance: %v", bs)
	}
	sections := sectionsByKey(t, bs, "sections")
	for key, want := range map[string]int64{
		"bank": 100000, "current_assets": 10000, "fixed_assets": 20000, "inventory": 5000,
		"prepayments": 2000, "non_current_assets": 3000, "current_liabilities": 9000,
		"non_current_liabilities": 6000, "equity": 125000,
	} {
		section, ok := sections[key]
		if !ok {
			t.Fatalf("missing balance sheet section %s: %v", key, sections)
		}
		if got := cents(t, section, "total_cents"); got != want {
			t.Errorf("section %s total = %d, want %d", key, got, want)
		}
	}
	// Equity holds the retained earnings account as its own line and the computed profit as another;
	// the section total counts each once.
	var retained, computed int64
	for _, entry := range sections["equity"]["lines"].([]any) {
		line := entry.(map[string]any)
		if line["class"] == "retained_earnings" && line["computed"] == nil {
			retained = int64(line["amount_cents"].(float64))
		}
		if line["computed"] == true {
			computed = int64(line["amount_cents"].(float64))
		}
	}
	if retained != 5000 || computed != 113000 {
		t.Fatalf("retained = %d, computed = %d, want 5000 and 113000", retained, computed)
	}
}

func TestAnalyticsReconcilesToProfitAndLoss(t *testing.T) {
	h, owner, org, _ := shapeFixture(t, "shape-analytics@example.com")
	pl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+org+"&from=2026-01-01&to=2026-12-31", "", owner), 200)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/reports/analytics?org_id="+org+"&from=2026-01-01&to=2026-12-31", "", owner), 200)
	totals := got["totals"].(map[string]any)
	for _, key := range []string{"revenue_cents", "expense_cents", "gross_profit_cents", "other_income_cents", "other_expense_cents"} {
		if cents(t, totals, key) != cents(t, pl, key) {
			t.Errorf("analytics %s = %d, P&L = %d", key, cents(t, totals, key), cents(t, pl, key))
		}
	}
	// The home tile net figure is the P&L net profit, not the old revenue minus expense.
	if cents(t, totals, "net_cents") != 113000 || cents(t, totals, "net_cents") != cents(t, pl, "profit_cents") {
		t.Fatalf("analytics net = %d, P&L profit = %d", cents(t, totals, "net_cents"), cents(t, pl, "profit_cents"))
	}
	series := got["series"].([]any)
	if len(series) != 1 {
		t.Fatalf("series = %v", series)
	}
	month := series[0].(map[string]any)
	if month["revenue_cents"].(float64) != 157000 || month["expense_cents"].(float64) != 50000 {
		t.Fatalf("month = %v", month)
	}
}
