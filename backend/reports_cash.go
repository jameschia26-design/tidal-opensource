package main

import (
	"context"
	"net/http"
)

// cashSummary: GET /api/v1/reports/cash-summary?org_id&from&to — Bank account (1000) opening, received, spent, closing.
// ponytail: system Bank account only, no comparison or monthly average; add per-bank-account support when accounts are flagged as bank.
func (a *app) cashSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	open, rec, sp, err := a.cashFor(r.Context(), access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT to_char(j.entry_date,'YYYY-MM'), COALESCE(SUM(jl.debit_cents),0), COALESCE(SUM(jl.credit_cents),0)
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND a.code=$4 AND j.entry_date BETWEEN $2::date AND $3::date
		GROUP BY 1 ORDER BY 1`, access.ID, from, to, acctBank)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	monthly := []map[string]any{}
	running := open
	for rows.Next() {
		var month string
		var debit, credit int64
		if err := rows.Scan(&month, &debit, &credit); err != nil {
			serverError(w, err)
			return
		}
		running += debit - credit
		monthly = append(monthly, map[string]any{"month": month, "received_cents": debit, "spent_cents": credit, "net_cents": debit - credit, "closing_cents": running})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	months := len(monthly)
	var avg int64
	if months > 0 {
		avg = (rec - sp) / int64(months)
	}
	f, _ := parseDay(from)
	t, _ := parseDay(to)
	days := int(t.Sub(f).Hours()/24) + 1
	pTo := f.AddDate(0, 0, -1)
	pFrom := pTo.AddDate(0, 0, -(days - 1))
	pOpen, pRec, pSp, err := a.cashFor(r.Context(), access.ID, day(pFrom), day(pTo))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"opening_cents": open, "received_cents": rec, "spent_cents": sp, "closing_cents": open + rec - sp, "net_cents": rec - sp,
		"monthly": monthly, "average_net_cents": avg,
		"previous": map[string]any{"from": day(pFrom), "to": day(pTo), "opening_cents": pOpen, "received_cents": pRec, "spent_cents": pSp, "net_cents": pRec - pSp, "closing_cents": pOpen + pRec - pSp},
	})
}

// cashFor returns Bank account (1000) opening balance and period debits/credits from posted journals.
func (a *app) cashFor(ctx context.Context, orgID int64, from, to string) (open, rec, sp int64, err error) {
	err = a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN j.entry_date < $2::date THEN jl.debit_cents-jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.credit_cents END),0)
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND a.code=$4 AND j.entry_date <= $3::date`, orgID, from, to, acctBank).Scan(&open, &rec, &sp)
	return
}
