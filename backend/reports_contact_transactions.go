package main

import (
	"net/http"
	"strconv"
)

// contactTransactions: GET /api/v1/reports/contact-transactions?org_id&contact_id&from&to
// One contact's sales and purchase movements in a period, totalled by kind. Xero requires a contact here.
// ponytail: totals only, no per-document rows or opening balance; reuse document-activity for the row list.
func (a *app) contactTransactions(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	contact, err := strconv.ParseInt(q.Get("contact_id"), 10, 64)
	if err != nil || contact <= 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var name string
	if err := a.db.QueryRow(ctx, `SELECT name FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, contact).Scan(&name); err != nil {
		writeError(w, 404, "not_found")
		return
	}
	rows, err := a.db.Query(ctx, `SELECT kind, COUNT(*), COALESCE(SUM(amount_cents),0)::bigint FROM (
		SELECT 'Invoice' kind, i.total_cents amount_cents FROM invoices i
			WHERE i.org_id=$1 AND i.contact_id=$2 AND i.status='posted' AND i.invoice_date BETWEEN $3::date AND $4::date
		UNION ALL
		SELECT 'Payment', -p.amount_cents FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id
			WHERE p.org_id=$1 AND i.contact_id=$2 AND p.payment_date BETWEEN $3::date AND $4::date
		UNION ALL
		SELECT 'Credit note', -n.amount_cents FROM credit_notes n
			WHERE n.org_id=$1 AND n.contact_id=$2 AND n.credit_date BETWEEN $3::date AND $4::date
		UNION ALL
		SELECT 'Bill', b.total_cents FROM bills b
			WHERE b.org_id=$1 AND b.contact_id=$2 AND b.status='posted' AND b.bill_date BETWEEN $3::date AND $4::date
		UNION ALL
		SELECT 'Bill payment', -bp.amount_cents FROM bill_payments bp JOIN bills b ON b.org_id=bp.org_id AND b.id=bp.bill_id
			WHERE bp.org_id=$1 AND b.contact_id=$2 AND bp.payment_date BETWEEN $3::date AND $4::date
	) m GROUP BY kind ORDER BY kind`, access.ID, contact, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Kind   string `json:"kind"`
		Count  int64  `json:"count"`
		Amount int64  `json:"amount_cents"`
	}
	out := []row{}
	var total int64
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.Kind, &v.Count, &v.Amount); err != nil {
			serverError(w, err)
			return
		}
		total += v.Amount
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"contact_id": contact, "contact": name, "rows": out, "net_cents": total})
}
