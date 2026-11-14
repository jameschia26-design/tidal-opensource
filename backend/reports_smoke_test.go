package main

import (
	"testing"
)

// TestReportEndpointsRunWithData is the Phase 4 acceptance sweep: every implemented report endpoint is
// called against one small company that has a posted invoice, a posted bill and a payment, and must
// answer 200. The balances themselves are checked where the report has a self-proving identity
// (debits equal credits, assets equal liabilities plus equity), because a page existing is not
// evidence that its arithmetic works.
func TestReportEndpointsRunWithData(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "reports-smoke@example.com")
	org := orgFromList(t, a, owner, `{"name":"Reports smoke","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Smoke customer"}`, owner), 201)["id"].(float64)))
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Smoke supplier"}`, owner), 201)["id"].(float64)))

	account := func(code, name, kind string) string {
		return itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"`+code+`","name":"`+name+`","type":"`+kind+`"}`, owner), 201)["id"].(float64)))
	}
	revenue := account("4199", "Smoke sales", "revenue")
	expense := account("5199", "Smoke costs", "expense")
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","revenue_account_id":`+revenue+`,"quantity":2,"unit_price_cents":10000}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/payments", `{"org_id":`+o+`,"amount_cents":5000,"payment_date":"2026-03-15","idempotency_key":"smoke-1"}`, owner), 201)
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"SB-1","bill_date":"2026-03-02","due_date":"2026-03-31","lines":[{"description":"Costs","account_id":`+expense+`,"quantity":1,"unit_price_cents":4000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	common := "org_id=" + o + "&from=2026-01-01&to=2026-12-31&as_of=2026-12-31"
	paths := []string{
		"trial-balance", "accountant-pack", "pnl", "contact-activity", "documents", "document-lines",
		"project-time", "cash-summary", "executive-summary", "bank-summary", "cash-flows-direct",
		"movements-in-equity", "expense-claim-detail", "billable-expenses-outstanding",
		"tracking-summary", "document-activity", "gst-audit", "sales-tax", "tax-reconciliation",
		"sales-by-item", "bank-reconciliation", "project-financials", "contact-transactions",
		"inventory-items", "inventory-summary", "inventory-details", "analytics", "budget-summary",
		"budget-variance", "journals", "cash-validation", "gl-exceptions", "fixed-asset-reconciliation",
		"duplicate-statement-lines", "fixed-assets", "disposal-schedule", "receivables-aging",
		"sales-by-customer", "payables-aging", "balance-sheet", "ledger-lines", "watchlist",
		"ledger-summary", "fx-gains-losses", "gst-f5",
	}
	// Reports that take a scope or a specific party need it named; everything else runs on the date range.
	extra := map[string]string{
		"documents":            "&kind=receivable",
		"document-lines":       "&kind=receivable",
		"document-activity":    "&kind=receivable",
		"contact-transactions": "&contact_id=" + cust,
		"project-time":         "&status=all",
	}
	for _, path := range paths {
		res := do(t, h, "GET", "/api/v1/reports/"+path+"?"+common+extra[path], "", owner)
		if res.Code != 200 {
			t.Errorf("%s: status %d, body %s", path, res.Code, res.Body.String())
		}
	}

	// Two identities every accounting report set must satisfy.
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?"+common, "", owner), 200)
	var debit, credit int64
	if rows, ok := tb["rows"].([]any); ok {
		for _, r := range rows {
			row := r.(map[string]any)
			debit += int64(row["debit_cents"].(float64))
			credit += int64(row["credit_cents"].(float64))
		}
	}
	if debit != credit {
		t.Errorf("trial balance does not balance: debit %d credit %d", debit, credit)
	}
	bs := expectStatus(t, do(t, h, "GET", "/api/v1/reports/balance-sheet?"+common, "", owner), 200)
	if bs["total_assets_cents"] != nil && bs["total_liabilities_cents"] != nil {
		assets := int64(bs["total_assets_cents"].(float64))
		liabilities := int64(bs["total_liabilities_cents"].(float64))
		equity := int64(0)
		if v, ok := bs["total_equity_cents"].(float64); ok {
			equity = int64(v)
		}
		if assets != liabilities+equity {
			t.Errorf("balance sheet does not balance: assets %d liabilities %d equity %d", assets, liabilities, equity)
		}
	}
}
