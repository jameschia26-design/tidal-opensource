package main

import "net/http"

// duplicateStatementLines: GET /api/v1/reports/duplicate-statement-lines?org_id&from&to
// Lists imported statement lines sharing date and amount. Exact repeats never reach the table (import_key), so these differ in text or reference.
// ponytail: no bank-account scope (statements are not tied to one) and no column chooser; add when statements carry a bank account.
func (a *app) duplicateStatementLines(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT l.id,l.line_date::text,l.description,l.reference,l.amount_cents,l.matched_payment_id IS NOT NULL
		FROM bank_statement_lines l WHERE l.org_id=$1 AND l.line_date BETWEEN $2::date AND $3::date
		AND (SELECT count(*) FROM bank_statement_lines o WHERE o.org_id=l.org_id AND o.line_date=l.line_date AND o.amount_cents=l.amount_cents)>1
		ORDER BY l.line_date,l.amount_cents,l.id LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type line struct {
		ID          int64  `json:"id"`
		Date        string `json:"date"`
		Description string `json:"description"`
		Reference   string `json:"reference"`
		AmountCents int64  `json:"amount_cents"`
		Matched     bool   `json:"matched"`
	}
	out := []line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.ID, &l.Date, &l.Description, &l.Reference, &l.AmountCents, &l.Matched); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 500
	if truncated {
		out = out[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out, "truncated": truncated})
}
