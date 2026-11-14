package main

import (
	"math"
	"net/http"
)

// Analytics reports back the screens Xero serves from its Analytics product: a monthly revenue and
// spending series, headline totals, a cash forecast from due documents, and a small set of ratios.
// Everything comes from posted ledger lines and open documents, so the numbers agree with the ledger
// and ageing reports. Tidal has no separate analytics warehouse and no custom chart builder.
// ponytail: fixed set of measures; add a saved-dashboard table when a company needs to choose its own.

type analyticsMonth struct {
	Month            string `json:"month"`
	RevenueCents     int64  `json:"revenue_cents"`
	CostOfSalesCents int64  `json:"cost_of_sales_cents"`
	ExpenseCents     int64  `json:"expense_cents"`
	GrossProfitCents int64  `json:"gross_profit_cents"`
}

type analyticsTotals struct {
	RevenueCents      int64 `json:"revenue_cents"`
	ExpenseCents      int64 `json:"expense_cents"`
	GrossProfitCents  int64 `json:"gross_profit_cents"`
	OtherIncomeCents  int64 `json:"other_income_cents"`
	OtherExpenseCents int64 `json:"other_expense_cents"`
	NetCents          int64 `json:"net_cents"`
	BankCents         int64 `json:"bank_cents"`
	ReceivableCents   int64 `json:"receivable_cents"`
	PayableCents      int64 `json:"payable_cents"`
	AssetCents        int64 `json:"asset_cents"`
	LiabilityCents    int64 `json:"liability_cents"`
}

type analyticsRatios struct {
	NetMarginBP      int64 `json:"net_margin_bp"`
	CurrentRatioBP   int64 `json:"current_ratio_bp"`
	DebtorDays       int64 `json:"debtor_days"`
	CreditorDays     int64 `json:"creditor_days"`
	CashRunwayMonths int64 `json:"cash_runway_months"`
}

type analyticsForecast struct {
	Month    string `json:"month"`
	InCents  int64  `json:"in_cents"`
	OutCents int64  `json:"out_cents"`
}

func bp(part, whole int64) int64 {
	if whole == 0 {
		return 0
	}
	return part * 10000 / whole
}

// analytics: GET /api/v1/reports/analytics?org_id&from&to
func (a *app) analytics(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	// A period that ends in the future still reports only what has happened by today.
	to = reportEnd(r, to)
	fromDay, _ := parseDay(from)
	toDay, _ := parseDay(to)
	if toDay.Before(fromDay) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()

	// Group by account class so the home tile and the dashboards show the same revenue, cost of
	// sales, expenses, other income and other expense the profit and loss report does.
	series := []analyticsMonth{}
	rows, err := a.db.Query(ctx, `SELECT to_char(j.entry_date,'YYYY-MM') m, a.class,
		COALESCE(SUM(jl.credit_cents-jl.debit_cents),0)::bigint
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date AND a.type IN ('revenue','expense')
		GROUP BY 1,2 ORDER BY 1`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	var totals analyticsTotals
	var costOfSales int64
	byMonth := map[string]*analyticsMonth{}
	monthOrder := []string{}
	for rows.Next() {
		var month, class string
		var creditCents int64
		if err := rows.Scan(&month, &class, &creditCents); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		m := byMonth[month]
		if m == nil {
			m = &analyticsMonth{Month: month}
			byMonth[month] = m
			monthOrder = append(monthOrder, month)
		}
		switch class {
		case "sales", "revenue":
			totals.RevenueCents += creditCents
			m.RevenueCents += creditCents
		case "other_income":
			totals.OtherIncomeCents += creditCents
		case "cost_of_sales":
			costOfSales -= creditCents
			totals.ExpenseCents -= creditCents
			m.CostOfSalesCents -= creditCents
			m.ExpenseCents -= creditCents
		case "expense":
			totals.ExpenseCents -= creditCents
			m.ExpenseCents -= creditCents
		case "other_expense":
			totals.OtherExpenseCents -= creditCents
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	for _, month := range monthOrder {
		m := byMonth[month]
		m.GrossProfitCents = m.RevenueCents - m.CostOfSalesCents
		series = append(series, *m)
	}
	totals.GrossProfitCents = totals.RevenueCents - costOfSales
	totals.NetCents = totals.RevenueCents + totals.OtherIncomeCents - totals.ExpenseCents - totals.OtherExpenseCents

	// Bank closing balance and asset/liability totals at the period end.
	err = a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN a.type='asset' THEN jl.debit_cents-jl.credit_cents END),0)::bigint,
		COALESCE(SUM(CASE WHEN a.type='liability' THEN jl.credit_cents-jl.debit_cents END),0)::bigint
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date<=$2::date`, access.ID, to).Scan(&totals.AssetCents, &totals.LiabilityCents)
	if err != nil {
		serverError(w, err)
		return
	}
	open, rec, sp, err := a.cashFor(ctx, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	totals.BankCents = open + rec - sp

	// Open receivables and payables use the same document maths as the ageing report.
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(i.total_cents
			- COALESCE((SELECT sum(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date<=$2::date),0)
			- COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date<=$2::date),0)
			- COALESCE((SELECT sum(ca.amount_cents) FROM customer_credit_applications ca WHERE ca.org_id=i.org_id AND ca.invoice_id=i.id AND ca.applied_on<=$2::date),0)),0)::bigint
		 FROM invoices i WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date<=$2::date`, access.ID, to).Scan(&totals.ReceivableCents); err != nil {
		serverError(w, err)
		return
	}
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(b.total_cents
			- COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$2::date),0)),0)::bigint
		 FROM bills b WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date<=$2::date`, access.ID, to).Scan(&totals.PayableCents); err != nil {
		serverError(w, err)
		return
	}

	// Six-month forecast: documents due after the period end, by due month.
	forecast := []analyticsForecast{}
	rows, err = a.db.Query(ctx, `SELECT to_char(due_date,'YYYY-MM') m,
		COALESCE(SUM(CASE WHEN kind='in' THEN balance END),0)::bigint,
		COALESCE(SUM(CASE WHEN kind='out' THEN balance END),0)::bigint
		FROM (
			SELECT i.due_date, 'in' kind, i.total_cents
				- COALESCE((SELECT sum(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date<=$2::date),0)
				- COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date<=$2::date),0)
				- COALESCE((SELECT sum(ca.amount_cents) FROM customer_credit_applications ca WHERE ca.org_id=i.org_id AND ca.invoice_id=i.id AND ca.applied_on<=$2::date),0) balance
				FROM invoices i WHERE i.org_id=$1 AND i.status='posted' AND i.due_date>$2::date AND i.due_date<=$3::date
			UNION ALL
			SELECT b.due_date, 'out', b.total_cents
				- COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$2::date),0)
				FROM bills b WHERE b.org_id=$1 AND b.status='posted' AND b.due_date>$2::date AND b.due_date<=$3::date
		) d WHERE balance>0 GROUP BY 1 ORDER BY 1`, access.ID, to, toDay.AddDate(0, 6, 0).Format("2006-01-02"))
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var f analyticsForecast
		if err := rows.Scan(&f.Month, &f.InCents, &f.OutCents); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		forecast = append(forecast, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	monthlySpend := totals.ExpenseCents
	if months := int64(math.Max(1, math.Ceil(toDay.Sub(fromDay).Hours()/24/30.4))); monthlySpend > 0 {
		monthlySpend /= months
	}
	ratios := analyticsRatios{
		NetMarginBP:    bp(totals.NetCents, totals.RevenueCents),
		CurrentRatioBP: bp(totals.AssetCents, totals.LiabilityCents),
		CashRunwayMonths: func() int64 {
			if monthlySpend <= 0 {
				return 0
			}
			return totals.BankCents / monthlySpend
		}(),
	}
	if days := toDay.Sub(fromDay).Hours() / 24; days > 0 {
		ratios.DebtorDays = int64(float64(totals.ReceivableCents) / float64(totals.RevenueCents) * 365)
		ratios.CreditorDays = int64(float64(totals.PayableCents) / float64(totals.ExpenseCents) * 365)
	}
	writeJSON(w, 200, map[string]any{"from": from, "to": to, "series": series, "totals": totals, "ratios": ratios, "forecast": forecast})
}
