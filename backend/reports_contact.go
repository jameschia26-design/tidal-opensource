package main

import "net/http"

// contactActivity is document activity, not a full ledger allocation by contact.
func (a *app) contactActivity(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, ok := parseDay(r.URL.Query().Get("from"))
	if !ok {
		writeError(w, 400, "invalid_input")
		return
	}
	to, ok := parseDay(r.URL.Query().Get("to"))
	if !ok || to.Before(from) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `WITH activity AS (
 SELECT contact_id,total_cents AS income,0::bigint AS expense FROM invoices WHERE org_id=$1 AND status='posted' AND invoice_date BETWEEN $2 AND $3
 UNION ALL SELECT contact_id,total_cents,0 FROM sales_receipts WHERE org_id=$1 AND receipt_date BETWEEN $2 AND $3
 UNION ALL SELECT contact_id,-amount_cents,0 FROM credit_notes WHERE org_id=$1 AND credit_date BETWEEN $2 AND $3
 UNION ALL SELECT contact_id,-total_cents,0 FROM customer_credit_notes WHERE org_id=$1 AND status='open' AND credit_date BETWEEN $2 AND $3
 UNION ALL SELECT contact_id,0,total_cents FROM bills WHERE org_id=$1 AND status='posted' AND bill_date BETWEEN $2 AND $3
 ) SELECT c.id,c.name,sum(activity.income),sum(activity.expense) FROM activity JOIN contacts c ON c.org_id=$1 AND c.id=activity.contact_id GROUP BY c.id,c.name ORDER BY lower(c.name),c.id`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ContactID int64  `json:"contact_id"`
		Contact   string `json:"contact"`
		Income    int64  `json:"income_cents"`
		Expense   int64  `json:"expense_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ContactID, &v.Contact, &v.Income, &v.Expense); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"rows": out})
}
