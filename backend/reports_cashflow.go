package main

import "net/http"

// bankAccountFilter lists accounts the user flagged as bank accounts (R16), not a code range.
const bankAccountFilter = `a.is_bank_account`

// bankSummary: GET /api/v1/reports/bank-summary?org_id&from&to — opening, received, spent and closing per bank account.
func (a *app) bankSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT a.code,a.name,a.type,a.is_bank_account,
		COALESCE(SUM(CASE WHEN j.entry_date < $2::date THEN jl.debit_cents-jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.credit_cents END),0)
		FROM accounts a LEFT JOIN journal_lines jl ON jl.org_id=a.org_id AND jl.account_id=a.id
		LEFT JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id AND j.entry_date<=$3::date
		WHERE a.org_id=$1 AND `+bankAccountFilter+`
		GROUP BY a.id,a.code,a.name,a.type,a.is_bank_account ORDER BY a.code`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Type     string `json:"type"`
		Status   string `json:"status"`
		Opening  int64  `json:"opening_cents"`
		Received int64  `json:"received_cents"`
		Spent    int64  `json:"spent_cents"`
		Closing  int64  `json:"closing_cents"`
	}
	out := []row{}
	var tot row
	for rows.Next() {
		var x row
		var bank bool
		if err := rows.Scan(&x.Code, &x.Name, &x.Type, &bank, &x.Opening, &x.Received, &x.Spent); err != nil {
			serverError(w, err)
			return
		}
		x.Status = "Not a bank account"
		if bank {
			x.Status = "Bank"
		}
		x.Closing = x.Opening + x.Received - x.Spent
		tot.Opening += x.Opening
		tot.Received += x.Received
		tot.Spent += x.Spent
		tot.Closing += x.Closing
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "total": tot})
}

// cashFlowsDirect: GET /api/v1/reports/cash-flows-direct?org_id&from&to — cash movement grouped by the counter account.
// ponytail: journals naming several non-cash accounts are grouped as "Split entries"; no operating/investing/financing split.
func (a *app) cashFlowsDirect(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	var opening int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0)
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND `+bankAccountFilter+` AND j.entry_date<$2::date`, access.ID, from).Scan(&opening); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(ctx, `WITH bj AS (
		SELECT jl.journal_id, SUM(jl.debit_cents-jl.credit_cents) net
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND `+bankAccountFilter+` AND j.entry_date BETWEEN $2::date AND $3::date
		GROUP BY jl.journal_id),
	o AS (SELECT x.journal_id, count(DISTINCT x.account_id) n, min(x.account_id) acct FROM journal_lines x JOIN accounts a ON a.org_id=x.org_id AND a.id=x.account_id
		WHERE x.org_id=$1 AND NOT (`+bankAccountFilter+`) AND x.journal_id IN (SELECT journal_id FROM bj) GROUP BY x.journal_id)
	SELECT COALESCE(MAX(a.code),''), COALESCE(MAX(a.name),'Split entries'),
		COALESCE(SUM(CASE WHEN bj.net>0 THEN bj.net END),0), COALESCE(SUM(CASE WHEN bj.net<0 THEN -bj.net END),0)
	FROM bj JOIN o ON o.journal_id=bj.journal_id
	LEFT JOIN accounts a ON a.org_id=$1 AND a.id=o.acct AND o.n=1
	GROUP BY CASE WHEN o.n=1 THEN o.acct ELSE 0 END ORDER BY 1`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Code string `json:"code"`
		Name string `json:"name"`
		In   int64  `json:"in_cents"`
		Out  int64  `json:"out_cents"`
		Net  int64  `json:"net_cents"`
	}
	out := []row{}
	var in, outSum int64
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Code, &x.Name, &x.In, &x.Out); err != nil {
			serverError(w, err)
			return
		}
		x.Net = x.In - x.Out
		in += x.In
		outSum += x.Out
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opening_cents": opening, "rows": out,
		"in_cents": in, "out_cents": outSum, "net_cents": in - outSum, "closing_cents": opening + in - outSum})
}

// movementsInEquity: GET /api/v1/reports/movements-in-equity?org_id&from&to — opening, movement and closing per equity account.
// ponytail: no compare period or column chooser; add when equity needs a prior-period column.
func (a *app) movementsInEquity(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT a.code,a.name,
		COALESCE(SUM(CASE WHEN j.entry_date < $2::date THEN jl.credit_cents-jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.credit_cents-jl.debit_cents END),0)
		FROM accounts a LEFT JOIN journal_lines jl ON jl.org_id=a.org_id AND jl.account_id=a.id
		LEFT JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id AND j.entry_date<=$3::date
		WHERE a.org_id=$1 AND a.type='equity' GROUP BY a.id,a.code,a.name ORDER BY a.code`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Code    string `json:"code"`
		Name    string `json:"name"`
		Opening int64  `json:"opening_cents"`
		Move    int64  `json:"movement_cents"`
		Closing int64  `json:"closing_cents"`
	}
	out := []row{}
	var totOpen, totClose int64
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Code, &x.Name, &x.Opening, &x.Move); err != nil {
			serverError(w, err)
			return
		}
		x.Closing = x.Opening + x.Move
		totOpen += x.Opening
		totClose += x.Closing
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "opening_cents": totOpen, "closing_cents": totClose})
}
