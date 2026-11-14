package main

import (
	"net/http"
	"time"
)

// budgetLine is one account's monthly budget amount.
type budgetLine struct {
	AccountID   int64 `json:"account_id"`
	AmountCents int64 `json:"amount_cents"`
}

type budgetInput struct {
	OrgID       int64        `json:"org_id"`
	PeriodMonth string       `json:"period_month"`
	Lines       []budgetLine `json:"lines"`
}

// saveBudget: PUT /api/v1/budgets — upsert one month of budget amounts. A zero amount clears that account's month.
func (a *app) saveBudget(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in budgetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	d, ok := parseDay(in.PeriodMonth)
	if !ok || d.Day() != 1 || len(in.Lines) == 0 || len(in.Lines) > 500 {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	seen := map[int64]bool{}
	for _, l := range in.Lines {
		if l.AccountID <= 0 || seen[l.AccountID] || l.AmountCents > maxMoneyCents || l.AmountCents < -maxMoneyCents {
			writeError(w, 400, "invalid_input")
			return
		}
		seen[l.AccountID] = true
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type IN ('revenue','expense'))`, access.ID, l.AccountID).Scan(&valid); err != nil {
			serverError(w, err)
			return
		}
		if !valid {
			writeError(w, 422, "invalid_account")
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO budgets(org_id,period_month,account_id,amount_cents,updated_by) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (org_id,period_month,account_id) DO UPDATE SET amount_cents=EXCLUDED.amount_cents,updated_by=EXCLUDED.updated_by,updated_at=now()`,
			access.ID, day(d), l.AccountID, l.AmountCents, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'budget.saved')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"period_month": day(d), "lines": in.Lines})
}

// listBudgets: GET /api/v1/budgets?org_id&from&to — budget rows with account code and name for a month range.
func (a *app) listBudgets(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT b.account_id,a.code,a.name,a.type,to_char(b.period_month,'YYYY-MM'),b.amount_cents
		FROM budgets b JOIN accounts a ON a.org_id=b.org_id AND a.id=b.account_id
		WHERE b.org_id=$1 AND b.period_month BETWEEN date_trunc('month',$2::date) AND date_trunc('month',$3::date)
		ORDER BY a.code,b.period_month LIMIT 5000`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		AccountID int64  `json:"account_id"`
		Code      string `json:"code"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		Month     string `json:"period_month"`
		Amount    int64  `json:"amount_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.AccountID, &v.Code, &v.Name, &v.Type, &v.Month, &v.Amount); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": out})
}

// budgetMonth is one column of a budget report.
type budgetMonth struct {
	Month  string `json:"month"` // YYYY-MM
	Amount int64  `json:"amount_cents"`
}

type budgetRow struct {
	AccountID   int64         `json:"account_id"`
	Code        string        `json:"code"`
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Months      []budgetMonth `json:"months"`
	TotalCents  int64         `json:"total_cents"`
	ActualCents int64         `json:"actual_cents,omitempty"`
}

// budgetRangeMonths returns every YYYY-MM between from and to inclusive, so an empty month still shows as a column.
func budgetRangeMonths(from, to time.Time) []string {
	out := []string{}
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); !m.After(to); m = m.AddDate(0, 1, 0) {
		out = append(out, m.Format("2006-01"))
	}
	return out
}

// budgetSummary: GET /api/v1/reports/budget-summary?org_id&from&to — budget amounts per account per month.
// ponytail: budget only, no actual or group subtotals; add them when the screen grows a compare column.
func (a *app) budgetSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT b.account_id,a.code,a.name,a.type,to_char(b.period_month,'YYYY-MM'),b.amount_cents
		FROM budgets b JOIN accounts a ON a.org_id=b.org_id AND a.id=b.account_id
		WHERE b.org_id=$1 AND b.period_month BETWEEN date_trunc('month',$2::date) AND date_trunc('month',$3::date)
		ORDER BY a.code,b.period_month LIMIT 5000`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	months := budgetRangeMonths(mustDay(from), mustDay(to))
	byAccount := map[int64]*budgetRow{}
	order := []int64{}
	for rows.Next() {
		var id int64
		var code, name, typ, month string
		var amount int64
		if err := rows.Scan(&id, &code, &name, &typ, &month, &amount); err != nil {
			serverError(w, err)
			return
		}
		if byAccount[id] == nil {
			byAccount[id] = &budgetRow{AccountID: id, Code: code, Name: name, Type: typ, Months: make([]budgetMonth, len(months))}
			order = append(order, id)
		}
		for i, m := range months {
			if m == month {
				byAccount[id].Months[i] = budgetMonth{Month: m, Amount: amount}
				byAccount[id].TotalCents += amount
			}
		}
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	out := []*budgetRow{}
	for _, id := range order {
		out = append(out, byAccount[id])
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "months": months, "rows": out})
}

// budgetVariance: GET /api/v1/reports/budget-variance?org_id&from&to — actual against budget per account.
// Actuals are posted journal movements: revenue is credits less debits, expenses are debits less credits.
func (a *app) budgetVariance(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	type row struct {
		AccountID   int64  `json:"account_id"`
		Code        string `json:"code"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		ActualCents int64  `json:"actual_cents"`
		BudgetCents int64  `json:"budget_cents"`
		Variance    int64  `json:"variance_cents"`
		VariancePct *int64 `json:"variance_pct"` // basis points of budget; null when budget is zero
	}
	rows, err := a.db.Query(r.Context(), `WITH actual AS (
			SELECT jl.account_id,
				SUM(CASE WHEN a.type='revenue' THEN jl.credit_cents-jl.debit_cents ELSE jl.debit_cents-jl.credit_cents END) AS cents
			FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
			JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
			WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date AND a.type IN ('revenue','expense')
			GROUP BY jl.account_id), budget AS (
			SELECT account_id,SUM(amount_cents) AS cents FROM budgets
			WHERE org_id=$1 AND period_month BETWEEN date_trunc('month',$2::date) AND date_trunc('month',$3::date)
			GROUP BY account_id)
		SELECT a.id,a.code,a.name,a.type,COALESCE(ac.cents,0),COALESCE(bu.cents,0)
		FROM accounts a
		LEFT JOIN actual ac ON ac.account_id=a.id
		LEFT JOIN budget bu ON bu.account_id=a.id
		WHERE a.org_id=$1 AND a.type IN ('revenue','expense') AND (ac.cents IS NOT NULL OR bu.cents IS NOT NULL)
		ORDER BY a.code LIMIT 500`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []row{}
	var actualRev, actualExp, budgetRev, budgetExp int64
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.AccountID, &v.Code, &v.Name, &v.Type, &v.ActualCents, &v.BudgetCents); err != nil {
			serverError(w, err)
			return
		}
		v.Variance = v.ActualCents - v.BudgetCents
		if v.BudgetCents != 0 {
			pct := v.Variance * 10000 / v.BudgetCents
			v.VariancePct = &pct
		}
		if v.Type == "revenue" {
			actualRev += v.ActualCents
			budgetRev += v.BudgetCents
		} else {
			actualExp += v.ActualCents
			budgetExp += v.BudgetCents
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	// Net profit is the one computed line: budget alone never carries it.
	netActual, netBudget := actualRev-actualExp, budgetRev-budgetExp
	var netPct *int64
	if netBudget != 0 {
		pct := (netActual - netBudget) * 10000 / netBudget
		netPct = &pct
	}
	totals := map[string]any{
		"actual_revenue_cents": actualRev, "budget_revenue_cents": budgetRev,
		"actual_expense_cents": actualExp, "budget_expense_cents": budgetExp,
		"net_actual_cents": netActual, "net_budget_cents": netBudget,
		"net_variance_cents": netActual - netBudget, "net_variance_pct": netPct,
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": out, "totals": totals})
}
