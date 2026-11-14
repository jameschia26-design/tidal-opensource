package main

import (
	"context"
	"net/http"
)

// trackingSummary: GET /api/v1/reports/tracking-summary?org_id&from&to — posted journal amounts grouped
// by tracking category option, for profit-and-loss accounts and balance-sheet accounts.
//
// The figures come from the same rows the profit and loss report reads, split by the values on each
// line, so a document whose lines carry two different options is counted per line and the rows plus
// the untracked line always reconcile to the P&L for the same period. Balance-sheet accounts report
// their opening, net movement and closing in the same row; the P&L columns stay zero for those rows
// and the balance columns stay zero for P&L rows, so a mixed option row carries both.
func (a *app) trackingSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	ctx := r.Context()
	type row struct {
		Category string `json:"category"`
		Option   string `json:"option"`
		Income   int64  `json:"income_cents"`
		Expense  int64  `json:"expense_cents"`
		Opening  int64  `json:"opening_cents"`
		Net      int64  `json:"net_cents"`
		Closing  int64  `json:"closing_cents"`
	}
	out := []row{}
	rs, err := a.db.Query(ctx, `SELECT c.name, o.name,
		COALESCE(SUM(CASE WHEN a.type='revenue' AND j.entry_date BETWEEN $2::date AND $3::date THEN jl.credit_cents-jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN a.type='expense' AND j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents-jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN a.type IN ('asset','liability','equity') AND j.entry_date < $2::date THEN jl.debit_cents-jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN a.type IN ('asset','liability','equity') AND j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents-jl.credit_cents END),0)
		FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		CROSS JOIN LATERAL jsonb_each_text(jl.tracking) kv
		JOIN tracking_options o ON o.org_id=jl.org_id AND o.id=kv.value::bigint AND o.category_id=kv.key::bigint
		JOIN tracking_categories c ON c.org_id=o.org_id AND c.id=o.category_id
		WHERE jl.org_id=$1 AND j.entry_date <= $3::date
		  AND a.type IN ('revenue','expense','asset','liability','equity')
		  AND kv.key ~ '^[0-9]+$' AND kv.value ~ '^[0-9]+$'
		GROUP BY c.name, o.name ORDER BY c.name, o.name LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rs.Close()
	var taggedIn, taggedOut, taggedOpen, taggedNet int64
	for rs.Next() {
		var x row
		if err := rs.Scan(&x.Category, &x.Option, &x.Income, &x.Expense, &x.Opening, &x.Net); err != nil {
			serverError(w, err)
			return
		}
		x.Closing = x.Opening + x.Net
		taggedIn += x.Income
		taggedOut += x.Expense
		taggedOpen += x.Opening
		taggedNet += x.Net
		out = append(out, x)
	}
	if err := rs.Err(); err != nil {
		serverError(w, err)
		return
	}
	// The same window and the same accounts as profitAndLoss, so the two reports always agree.
	var allIn, allOut int64
	if err := a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN a.type='revenue' THEN jl.credit_cents-jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN a.type='expense' THEN jl.debit_cents-jl.credit_cents END),0)
		FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date AND a.type IN ('revenue','expense')`,
		access.ID, from, to).Scan(&allIn, &allOut); err != nil {
		serverError(w, err)
		return
	}
	// Balance-sheet totals over the same window, so the untracked row carries the remainder.
	var bsOpen, bsNet int64
	if err := a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN j.entry_date < $2::date THEN jl.debit_cents-jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents-jl.credit_cents END),0)
		FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date <= $3::date AND a.type IN ('asset','liability','equity')`,
		access.ID, from, to).Scan(&bsOpen, &bsNet); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out,
		"untracked_income_cents": allIn - taggedIn, "untracked_expense_cents": allOut - taggedOut,
		"untracked_opening_cents": bsOpen - taggedOpen, "untracked_net_cents": bsNet - taggedNet,
		"untracked_closing_cents": bsOpen + bsNet - taggedOpen - taggedNet,
		"income_cents":            allIn, "expense_cents": allOut,
		"opening_cents": bsOpen, "net_cents": bsNet, "closing_cents": bsOpen + bsNet})
}

// pnlTracking builds the per-option columns for one tracking category: one row per profit-and-loss
// account, a column per option, plus the amount left untracked. Every row's columns sum to the
// account's P&L amount, which the reconciliation test asserts.
func (a *app) pnlTracking(ctx context.Context, orgID int64, from, to string, categoryID int64) (map[string]any, bool, error) {
	var name string
	var active bool
	err := a.db.QueryRow(ctx, `SELECT name, active FROM tracking_categories WHERE org_id=$1 AND id=$2`, orgID, categoryID).Scan(&name, &active)
	if err != nil {
		return nil, false, err
	}
	optRows, err := a.db.Query(ctx, `SELECT id, name FROM tracking_options WHERE org_id=$1 AND category_id=$2 ORDER BY archived, name`, orgID, categoryID)
	if err != nil {
		return nil, false, err
	}
	options := []map[string]any{}
	for optRows.Next() {
		var id int64
		var oname string
		if err := optRows.Scan(&id, &oname); err != nil {
			optRows.Close()
			return nil, false, err
		}
		options = append(options, map[string]any{"id": id, "name": oname})
	}
	optRows.Close()
	if err := optRows.Err(); err != nil {
		return nil, false, err
	}
	rows, err := a.db.Query(ctx, `SELECT a.code, a.name,
		COALESCE(o.name,''),
		COALESCE(SUM(CASE WHEN a.type='revenue' THEN jl.credit_cents-jl.debit_cents ELSE jl.debit_cents-jl.credit_cents END),0)
		FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		LEFT JOIN tracking_options o ON o.org_id=jl.org_id AND o.category_id=$4
			AND o.id = CASE WHEN jl.tracking->>($4::text) ~ '^[0-9]+$' THEN (jl.tracking->>($4::text))::bigint END
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date AND a.type IN ('revenue','expense')
		GROUP BY a.code, a.name, o.name ORDER BY a.code`, orgID, from, to, categoryID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	type trackRow struct {
		Code      string           `json:"code"`
		Name      string           `json:"name"`
		Amounts   map[string]int64 `json:"amounts"`
		Untracked int64            `json:"untracked_cents"`
	}
	byCode := map[string]*trackRow{}
	order := []string{}
	for rows.Next() {
		var code, aname, option string
		var cents int64
		if err := rows.Scan(&code, &aname, &option, &cents); err != nil {
			return nil, false, err
		}
		tr, seen := byCode[code]
		if !seen {
			tr = &trackRow{Code: code, Name: aname, Amounts: map[string]int64{}}
			byCode[code] = tr
			order = append(order, code)
		}
		if option == "" {
			tr.Untracked += cents
		} else {
			tr.Amounts[option] += cents
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	list := make([]trackRow, 0, len(order))
	for _, code := range order {
		list = append(list, *byCode[code])
	}
	return map[string]any{"category_id": categoryID, "category": name, "active": active,
		"options": options, "rows": list}, true, nil
}
