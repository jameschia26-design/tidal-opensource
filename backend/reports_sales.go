package main

import "net/http"

// salesByCustomer reports document totals, not revenue-account balances or cash received.
func (a *app) salesByCustomer(w http.ResponseWriter, r *http.Request, u currentUser) {
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
		SELECT contact_id,total_cents AS invoices,0::bigint AS receipts,0::bigint AS credits FROM invoices WHERE org_id=$1 AND status='posted' AND invoice_date BETWEEN $2 AND $3
		UNION ALL SELECT contact_id,0,total_cents,0 FROM sales_receipts WHERE org_id=$1 AND receipt_date BETWEEN $2 AND $3
		UNION ALL SELECT contact_id,0,0,amount_cents FROM credit_notes WHERE org_id=$1 AND credit_date BETWEEN $2 AND $3
		UNION ALL SELECT contact_id,0,0,total_cents FROM customer_credit_notes WHERE org_id=$1 AND status='open' AND credit_date BETWEEN $2 AND $3
	) SELECT c.id,c.name,sum(activity.invoices),sum(activity.receipts),sum(activity.credits)
	FROM activity JOIN contacts c ON c.org_id=$1 AND c.id=activity.contact_id
	GROUP BY c.id,c.name ORDER BY lower(c.name),c.id`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ContactID int64  `json:"contact_id"`
		Customer  string `json:"customer"`
		Invoices  int64  `json:"invoices_cents"`
		Receipts  int64  `json:"receipts_cents"`
		Credits   int64  `json:"credits_cents"`
		Net       int64  `json:"net_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ContactID, &v.Customer, &v.Invoices, &v.Receipts, &v.Credits); err != nil {
			serverError(w, err)
			return
		}
		v.Net = v.Invoices + v.Receipts - v.Credits
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"from": day(from), "to": day(to), "rows": out})
}
