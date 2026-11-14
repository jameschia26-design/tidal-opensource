package main

import (
	"net/http"
)

// watchlist backs the Xero dashboard watchlist: accounts flagged "show on dashboard"
// with their balance to date. Xero shows bank balances; here the balance is posted
// ledger movement, so an unpresented CSV statement line is not included.
func (a *app) watchlist(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	// Balances are "to date": future-dated postings do not count unless the caller asks for them.
	asOf := reportToday()
	if r.URL.Query().Get("include_future") == "1" {
		asOf = "9999-12-31"
	}
	rows, err := a.db.Query(r.Context(), `
SELECT a.id, a.code, a.name, a.type,
       COALESCE((SELECT SUM(jl.debit_cents - jl.credit_cents)
                 FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
                 WHERE jl.org_id=$1 AND jl.account_id=a.id AND j.entry_date <= $2::date), 0)
FROM accounts a
WHERE a.org_id=$1 AND a.show_on_dashboard
ORDER BY a.code`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type item struct {
		ID      int64  `json:"id"`
		Code    string `json:"code"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		Balance int64  `json:"balance_cents"`
	}
	out := []item{}
	for rows.Next() {
		var row item
		if err := rows.Scan(&row.ID, &row.Code, &row.Name, &row.Type, &row.Balance); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}
