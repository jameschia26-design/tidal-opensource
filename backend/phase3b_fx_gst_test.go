package main

import (
	"net/http"
	"testing"
)

// Phase 3b: currencies with dated rates, month-end revaluation, realised FX on settlement, the FX
// report, and GST return periods with filing state.

// seedSalesAccount creates the standard chart through the production seeding path and returns Sales.
func seedSalesAccount(t *testing.T, a *app, org int64) int64 {
	t.Helper()
	tx, err := a.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	ids, err := accountIDs(t.Context(), tx, org)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return ids[acctSales]
}

func fxOrg(t *testing.T, a *app, email string) (http.Handler, string, int64, int64, int64) {
	t.Helper()
	h := a.routes()
	owner := seedSession(t, a, email)
	org := orgFromList(t, a, owner, `{"name":"FX co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"USD buyer"}`, owner), 201)["id"].(float64))
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"USD seller"}`, owner), 201)["id"].(float64))
	return h, owner, org, cust, supp
}

func postUSDInvoice(t *testing.T, h http.Handler, owner string, org, cust, sales, net int64, date, body string) int64 {
	t.Helper()
	o := itoa(org)
	invBody := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"` + date + `","due_date":"` + date + `","lines":[{"description":"Work","quantity":1,"unit_price_cents":` + itoa(net) + `,"revenue_account_id":` + itoa(sales) + `}]` + body + `}`
	id := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invBody, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, `{}`, owner), 200)
	return id
}

func TestCurrencyRateLookupByDocumentDate(t *testing.T) {
	a, _ := testApp(t)
	h, owner, org, cust, _ := fxOrg(t, a, "fx-rate@example.com")
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies", `{"org_id":`+o+`,"code":"USD","name":"US dollar"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-01","rate_micro":1300000}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-04-01","rate_micro":1350000}`, owner), 200)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/currencies?org_id="+o, "", owner), 200)
	if list["base"] != "SGD" || len(list["currencies"].([]any)) != 2 {
		t.Fatalf("currency list %v", list)
	}

	sales := seedSalesAccount(t, a, org)
	march := postUSDInvoice(t, h, owner, org, cust, sales, 13000, "2026-03-15", `,"currency":"USD"`)
	marchDoc := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(march)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	if marchDoc["currency"] != "USD" || marchDoc["fx_rate_micro"].(float64) != 1300000 {
		t.Fatalf("March rate %v", marchDoc)
	}
	april := postUSDInvoice(t, h, owner, org, cust, sales, 13500, "2026-04-05", `,"currency":"USD"`)
	aprilDoc := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(april)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	if aprilDoc["fx_rate_micro"].(float64) != 1350000 {
		t.Fatalf("April rate %v", aprilDoc)
	}
	// A typed rate still wins, and a currency with no rate is still refused.
	typed := postUSDInvoice(t, h, owner, org, cust, sales, 10000, "2026-03-20", `,"currency":"USD","fx_rate_micro":1250000`)
	typedDoc := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(typed)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	if typedDoc["fx_rate_micro"].(float64) != 1250000 {
		t.Fatalf("typed rate %v", typedDoc)
	}
	body := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-05-01","due_date":"2026-05-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":1000}],"currency":"EUR"}`
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 400)
}

func fxBalance(t *testing.T, a *app, org int64, code string) int64 {
	t.Helper()
	var balance int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(jl.credit_cents-jl.debit_cents),0)
		FROM journal_lines jl JOIN accounts acc ON acc.org_id=jl.org_id AND acc.id=jl.account_id
		WHERE jl.org_id=$1 AND acc.code=$2`, org, code).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	return balance
}

func TestRevaluationPostsOnceAndReversesOnRerun(t *testing.T) {
	a, _ := testApp(t)
	h, owner, org, cust, _ := fxOrg(t, a, "fx-reval@example.com")
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies", `{"org_id":`+o+`,"code":"USD","name":"US dollar"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-01","rate_micro":1300000}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-31","rate_micro":1400000}`, owner), 200)
	sales := seedSalesAccount(t, a, org)
	// USD 10,000 carried at 1.30 = SGD 13,000. At 1.40 it is SGD 14,000, so the gain is 1,000.
	postUSDInvoice(t, h, owner, org, cust, sales, 13000, "2026-03-10", `,"currency":"USD","fx_rate_micro":1300000`)

	first := expectStatus(t, do(t, h, "POST", "/api/v1/fx/revalue", `{"org_id":`+o+`,"period_end":"2026-03-31"}`, owner), 200)
	if first["total_cents"].(float64) != 1000 {
		t.Fatalf("first revaluation %v", first)
	}
	if got := fxBalance(t, a, org, acctUnrealisedGain); got != 1000 {
		t.Fatalf("unrealised gain %d, want 1000", got)
	}
	second := expectStatus(t, do(t, h, "POST", "/api/v1/fx/revalue", `{"org_id":`+o+`,"period_end":"2026-03-31"}`, owner), 200)
	if second["reversal_journal_id"].(float64) == 0 {
		t.Fatalf("rerun did not reverse the first journal: %v", second)
	}
	if got := fxBalance(t, a, org, acctUnrealisedGain); got != 1000 {
		t.Fatalf("after rerun unrealised gain %d, want 1000 (no double posting)", got)
	}
	var runs int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM fx_revaluation_runs WHERE org_id=$1 AND period_end='2026-03-31'`, org).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("%d runs, want 1", runs)
	}

	// A new period rate replaces the old figure rather than stacking on it.
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-31","rate_micro":1450000}`, owner), 200)
	third := expectStatus(t, do(t, h, "POST", "/api/v1/fx/revalue", `{"org_id":`+o+`,"period_end":"2026-03-31"}`, owner), 200)
	if third["total_cents"].(float64) != 1500 {
		t.Fatalf("third revaluation %v", third)
	}
	if got := fxBalance(t, a, org, acctUnrealisedGain); got != 1500 {
		t.Fatalf("unrealised gain after repricing %d, want 1500", got)
	}
}

func TestRealisedFXOnPartialSettlement(t *testing.T) {
	a, _ := testApp(t)
	h, owner, org, cust, _ := fxOrg(t, a, "fx-realised@example.com")
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies", `{"org_id":`+o+`,"code":"USD","name":"US dollar"}`, owner), 201)
	sales := seedSalesAccount(t, a, org)
	// USD 10,000 at 1.30 = SGD 13,000. Two half settlements at 1.40 each clear AR and gain 500 each.
	inv := postUSDInvoice(t, h, owner, org, cust, sales, 13000, "2026-02-01", `,"currency":"USD","fx_rate_micro":1300000`)
	pay := func(amount, rate, seq int64) {
		t.Helper()
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/payments",
			`{"org_id":`+o+`,"amount_cents":`+itoa(amount)+`,"payment_date":"2026-02-10","idempotency_key":"fx-pay-`+itoa(seq)+`","settlement_rate_micro":`+itoa(rate)+`}`, owner), 201)
	}
	pay(6500, 1400000, 1)
	if got := fxBalance(t, a, org, acctRealisedGain); got != 500 {
		t.Fatalf("partial realised gain %d, want 500", got)
	}
	var ar int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0)
		FROM journal_lines jl JOIN accounts acc ON acc.org_id=jl.org_id AND acc.id=jl.account_id
		WHERE jl.org_id=$1 AND acc.code='1100'`, org).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	if ar != 6500 {
		t.Fatalf("receivable after half payment %d, want 6500", ar)
	}
	pay(6500, 1400000, 2)
	if got := fxBalance(t, a, org, acctRealisedGain); got != 1000 {
		t.Fatalf("full realised gain %d, want 1000", got)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0)
		FROM journal_lines jl JOIN accounts acc ON acc.org_id=jl.org_id AND acc.id=jl.account_id
		WHERE jl.org_id=$1 AND acc.code='1100'`, org).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	if ar != 0 {
		t.Fatalf("receivable after settlement %d, want 0", ar)
	}

	report := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fx-gains-losses?org_id="+o+"&from=2026-02-01&to=2026-02-28", "", owner), 200)
	if report["realised_cents"].(float64) != 1000 || report["reconciles"] != true {
		t.Fatalf("FX report %v", report)
	}
}

func TestFXReportReconcilesRealisedAndUnrealised(t *testing.T) {
	a, _ := testApp(t)
	h, owner, org, cust, _ := fxOrg(t, a, "fx-report@example.com")
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies", `{"org_id":`+o+`,"code":"USD","name":"US dollar"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-31","rate_micro":1400000}`, owner), 200)
	sales := seedSalesAccount(t, a, org)
	inv := postUSDInvoice(t, h, owner, org, cust, sales, 13000, "2026-03-10", `,"currency":"USD","fx_rate_micro":1300000`)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/payments",
		`{"org_id":`+o+`,"amount_cents":6500,"payment_date":"2026-03-20","idempotency_key":"fx-report-pay","settlement_rate_micro":1400000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/fx/revalue", `{"org_id":`+o+`,"period_end":"2026-03-31"}`, owner), 200)

	report := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fx-gains-losses?org_id="+o+"&from=2026-03-01&to=2026-03-31", "", owner), 200)
	if report["realised_cents"].(float64) != 500 || report["unrealised_cents"].(float64) != 500 {
		t.Fatalf("movements %v", report)
	}
	if report["total_cents"].(float64) != 1000 || report["reconciles"] != true {
		t.Fatalf("report does not reconcile: %v", report)
	}
	rows := report["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected realised and unrealised rows, got %v", rows)
	}
}

func setTaxPeriod(t *testing.T, a *app, h http.Handler, owner string, org int64, period string) {
	t.Helper()
	o := itoa(org)
	get := expectStatus(t, do(t, h, "GET", "/api/v1/tax/settings?org_id="+o, "", owner), 200)
	body := `{"org_id":` + o + `,"tax_basis":"accrual","tax_id":"` + get["tax_id"].(string) + `","tax_display_name":"GST","tax_period":"` + period + `","default_sales_treatment":"exclusive","default_purchase_treatment":"exclusive"}`
	expectStatus(t, do(t, h, "PUT", "/api/v1/tax/settings", body, owner), 200)
}

func TestGSTReturnPeriodBoundaries(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-period@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2025-01-01"}`)
	o := itoa(org)
	setTaxPeriod(t, a, h, owner, org, "monthly")

	list := expectStatus(t, do(t, h, "GET", "/api/v1/tax/gst-returns?org_id="+o+"&year=2026", "", owner), 200)
	rows := list["returns"].([]any)
	if len(rows) != 12 {
		t.Fatalf("monthly returns %d, want 12", len(rows))
	}
	jan := rows[0].(map[string]any)
	if jan["period_start"] != "2026-01-01" || jan["period_end"] != "2026-01-31" || jan["due_date"] != "2026-02-28" {
		t.Fatalf("January period %v", jan)
	}
	if jan["status"] != "draft" {
		t.Fatalf("new period status %v", jan)
	}

	setTaxPeriod(t, a, h, owner, org, "quarterly")
	quarter := expectStatus(t, do(t, h, "GET", "/api/v1/tax/gst-returns?org_id="+o+"&year=2026", "", owner), 200)["returns"].([]any)
	if len(quarter) != 4 {
		t.Fatalf("quarterly returns %d, want 4", len(quarter))
	}
	q1 := quarter[0].(map[string]any)
	if q1["period_end"] != "2026-03-31" || q1["due_date"] != "2026-04-30" {
		t.Fatalf("Q1 %v", q1)
	}
	// A period that does not end on the quarterly boundary is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-returns/file",
		`{"org_id":`+o+`,"period_start":"2026-01-01","period_end":"2026-02-28"}`, owner), 422)
}

func TestGSTF5BoxesFromLedgerAndFilingState(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-file@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST file co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2025-01-01"}`)
	o := itoa(org)
	setTaxPeriod(t, a, h, owner, org, "quarterly")
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	sales := seedSalesAccount(t, a, org)
	body := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-02-05","due_date":"2026-02-28","lines":[{"description":"Work","quantity":1,"unit_price_cents":100000,"revenue_account_id":` + itoa(sales) + `,"gst_code":"SR"}]}`
	inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, `{}`, owner), 200)

	draft := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-f5?org_id="+o+"&from=2026-01-01&to=2026-03-31", "", owner), 200)
	box := func(payload map[string]any, n string) int64 {
		for _, raw := range payload["boxes"].([]any) {
			row := raw.(map[string]any)
			if row["box"] == n {
				return int64(row["value_cents"].(float64))
			}
		}
		t.Fatalf("box %s missing: %v", n, payload)
		return 0
	}
	if draft["filed"] != false || box(draft, "6") != 9000 || box(draft, "8") != 9000 {
		t.Fatalf("draft F5 %v", draft)
	}

	filed := expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-returns/file",
		`{"org_id":`+o+`,"period_start":"2026-01-01","period_end":"2026-03-31"}`, owner), 201)
	if filed["filed"] != true || filed["filed_by"] == "" || filed["filed_at"] == "" {
		t.Fatalf("filing metadata %v", filed)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/tax/gst-returns?org_id="+o+"&year=2026", "", owner), 200)["returns"].([]any)
	q1 := list[0].(map[string]any)
	if q1["status"] != "filed" || q1["filed_by"] == "" {
		t.Fatalf("filed period in list %v", q1)
	}
	// Filing again for the same period is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-returns/file",
		`{"org_id":`+o+`,"period_start":"2026-01-01","period_end":"2026-03-31"}`, owner), 409)

	after := expectStatus(t, do(t, h, "GET", "/api/v1/reports/gst-f5?org_id="+o+"&from=2026-01-01&to=2026-03-31", "", owner), 200)
	if after["filed"] != true || box(after, "6") != 9000 {
		t.Fatalf("filed return changed %v", after)
	}
}
