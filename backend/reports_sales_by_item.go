package main

import "net/http"

// salesByItem reports posted sales document lines by item for the selected period.
func (a *app) salesByItem(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `WITH sales AS (
		SELECT l.item_id, l.description, l.quantity, l.amount_cents
		FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id
		WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2::date AND $3::date
		UNION ALL
		SELECT x.item_id, x.description, x.quantity, x.amount_cents
		FROM sales_receipts s CROSS JOIN LATERAL jsonb_to_recordset(s.lines) AS x(item_id bigint,description text,quantity bigint,amount_cents bigint)
		WHERE s.org_id=$1 AND s.receipt_date BETWEEN $2::date AND $3::date
		UNION ALL
		SELECT x.item_id, x.description, -x.quantity, -x.amount_cents
		FROM customer_credit_notes n CROSS JOIN LATERAL jsonb_to_recordset(n.lines) AS x(item_id bigint,description text,quantity bigint,amount_cents bigint)
		WHERE n.org_id=$1 AND n.status='open' AND n.credit_date BETWEEN $2::date AND $3::date
	)
	SELECT s.item_id, COALESCE(i.name, s.description), SUM(s.quantity)::bigint, SUM(s.amount_cents)::bigint
	FROM sales s LEFT JOIN items i ON i.org_id=$1 AND i.id=s.item_id
	GROUP BY s.item_id, COALESCE(i.name, s.description)
	ORDER BY lower(COALESCE(i.name, s.description)), s.item_id NULLS LAST`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ItemID   *int64 `json:"item_id"`
		Item     string `json:"item"`
		Quantity int64  `json:"quantity"`
		Sales    int64  `json:"sales_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ItemID, &v.Item, &v.Quantity, &v.Sales); err != nil {
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
