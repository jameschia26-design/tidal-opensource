package main

import (
	"net/http"
	"time"
)

// executiveSummary: GET /api/v1/reports/executive-summary?org_id&from&to — the period against the equal-length period just before it.
// ponytail: no direct-cost/gross-profit split (accounts are not classed), no assets/ratios/days sections, single compare period; add when account classes exist.
func (a *app) executiveSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	f, _ := parseDay(from)
	t, _ := parseDay(to)
	days := int(t.Sub(f).Hours()/24) + 1
	pTo := f.AddDate(0, 0, -1)
	pFrom := pTo.AddDate(0, 0, -(days - 1))
	type period struct {
		From               string `json:"from"`
		To                 string `json:"to"`
		Received           int64  `json:"cash_received_cents"`
		Spent              int64  `json:"cash_spent_cents"`
		Surplus            int64  `json:"cash_surplus_cents"`
		Closing            int64  `json:"closing_bank_cents"`
		Income             int64  `json:"income_cents"`
		Expenses           int64  `json:"expenses_cents"`
		Profit             int64  `json:"profit_cents"`
		Debtors            int64  `json:"receivables_cents"`
		Creditor           int64  `json:"payables_cents"`
		Sales              int64  `json:"sales_cents"`
		OtherIncome        int64  `json:"other_income_cents"`
		CostSales          int64  `json:"cost_of_sales_cents"`
		OtherExpense       int64  `json:"other_expense_cents"`
		GrossProfit        int64  `json:"gross_profit_cents"`
		GrossMarginBP      int64  `json:"gross_margin_bp"`
		NetMarginBP        int64  `json:"net_margin_bp"`
		DebtorDays         int64  `json:"debtor_days"`
		CreditorDays       int64  `json:"creditor_days"`
		Bank               int64  `json:"bank_cents"`
		CurrentAssets      int64  `json:"current_assets_cents"`
		FixedAssets        int64  `json:"fixed_assets_cents"`
		CurrentLiabilities int64  `json:"current_liabilities_cents"`
		Equity             int64  `json:"equity_cents"`
		Assets             int64  `json:"assets_cents"`
	}
	calc := func(from, to string) (period, bool) {
		p := period{From: from, To: to}
		open, rec, sp, err := a.cashFor(r.Context(), access.ID, from, to)
		if err != nil {
			serverError(w, err)
			return p, false
		}
		p.Received, p.Spent, p.Surplus, p.Closing = rec, sp, rec-sp, open+rec-sp
		pl, err := a.balances(r.Context(), access.ID, &from, to)
		if err != nil {
			serverError(w, err)
			return p, false
		}
		for _, b := range pl {
			switch b.Type {
			case "revenue":
				p.Income += b.Credit - b.Debit
			case "expense":
				p.Expenses += b.Debit - b.Credit
			}
			switch b.Class {
			case "sales":
				p.Sales += b.Credit - b.Debit
			case "other_income":
				p.OtherIncome += b.Credit - b.Debit
			case "cost_of_sales":
				p.CostSales += b.Debit - b.Credit
			case "other_expense":
				p.OtherExpense += b.Debit - b.Credit
			}
		}
		pd, _ := parseDay(to)
		pf, _ := parseDay(from)
		days := int64(pd.Sub(pf).Hours()/24) + 1
		p.Profit = p.Income - p.Expenses
		p.GrossProfit = p.Sales - p.CostSales
		if p.Sales != 0 {
			p.GrossMarginBP = p.GrossProfit * 10000 / p.Sales
		}
		if p.Income != 0 {
			p.NetMarginBP = p.Profit * 10000 / p.Income
		}
		bs, err := a.balances(r.Context(), access.ID, nil, to)
		if err != nil {
			serverError(w, err)
			return p, false
		}
		for _, b := range bs {
			switch b.Code {
			case acctAR:
				p.Debtors = b.Debit - b.Credit
			case acctAP:
				p.Creditor = b.Credit - b.Debit
			}
			switch b.Class {
			case "bank":
				p.Bank += b.Debit - b.Credit
			case "current_asset", "inventory", "prepayment":
				p.CurrentAssets += b.Debit - b.Credit
			case "fixed_asset", "non_current_asset":
				p.FixedAssets += b.Debit - b.Credit
			case "current_liability", "non_current_liability":
				p.CurrentLiabilities += b.Credit - b.Debit
			case "equity", "retained_earnings":
				p.Equity += b.Credit - b.Debit
			}
			switch b.Type {
			case "asset":
				p.Assets += b.Debit - b.Credit
			}
		}
		if p.Income != 0 {
			p.DebtorDays = p.Debtors * days / p.Income
		}
		if direct := p.CostSales + p.Expenses; direct != 0 {
			p.CreditorDays = p.Creditor * days / direct
		}
		return p, true
	}
	cur, ok := calc(from, to)
	if !ok {
		return
	}
	prev, ok := calc(pFrom.Format(time.DateOnly), pTo.Format(time.DateOnly))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": cur, "previous": prev})
}
