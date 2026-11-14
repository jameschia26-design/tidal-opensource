package main

import "net/http"

// ponytail: posted line detail only; add document adjustments, grouping and exports for Xero parity.
func (a *app) documentLines(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	kind := q.Get("kind")
	if kind != "receivable" && kind != "payable" {
		writeError(w, 400, "invalid_input")
		return
	}
	var contact *int64
	if s := q.Get("contact_id"); s != "" {
		id, valid := parseOrgID(w, s)
		if !valid {
			return
		}
		contact = &id
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	// Source names where the document came from. Recurring and subscription links are stored, so the
	// column is derived from them rather than guessed.
	query := `SELECT i.id,i.number::text,c.name,i.invoice_date::text,i.due_date::text,l.line_no,l.description,COALESCE(it.sku,''),l.quantity,l.unit_price_cents,(l.quantity*l.unit_price_cents*l.discount_basis_points+5000)/10000,l.amount_cents,l.gst_cents,l.amount_cents+l.gst_cents,i.total_cents,i.status,CASE WHEN i.subscription_id IS NOT NULL THEN 'Subscription invoice' WHEN i.recurring_template_id IS NOT NULL THEN 'Repeating invoice' ELSE 'Invoice' END FROM invoices i JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id JOIN invoice_lines l ON l.org_id=i.org_id AND l.invoice_id=i.id LEFT JOIN items it ON it.org_id=l.org_id AND it.id=l.item_id WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2 AND $3 AND ($4::bigint IS NULL OR i.contact_id=$4) ORDER BY i.invoice_date,i.id,l.line_no LIMIT 5001`
	if kind == "payable" {
		query = `SELECT b.id,b.supplier_reference,c.name,b.bill_date::text,b.due_date::text,l.line_no,l.description,COALESCE(it.sku,''),l.quantity,l.unit_price_cents,l.discount_cents,l.amount_cents,l.gst_cents,l.amount_cents+l.gst_cents,b.total_cents,b.status,CASE WHEN b.recurring_profile_id IS NOT NULL THEN 'Repeating bill' ELSE 'Bill' END FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id JOIN bill_lines l ON l.org_id=b.org_id AND l.bill_id=b.id LEFT JOIN items it ON it.org_id=l.org_id AND it.id=l.item_id WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date BETWEEN $2 AND $3 AND ($4::bigint IS NULL OR b.contact_id=$4) ORDER BY b.bill_date,b.id,l.line_no LIMIT 5001`
	}
	rows, err := a.db.Query(r.Context(), query, access.ID, from, to, contact)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type line struct {
		ID           int64  `json:"document_id"`
		Number       string `json:"number"`
		Contact      string `json:"contact"`
		Date         string `json:"date"`
		Due          string `json:"due_date"`
		LineNo       int    `json:"line_no"`
		Description  string `json:"description"`
		ItemCode     string `json:"item_code"`
		Quantity     int64  `json:"quantity"`
		UnitPrice    int64  `json:"unit_price_cents"`
		Discount     int64  `json:"discount_cents"`
		Amount       int64  `json:"amount_cents"`
		GST          int64  `json:"gst_cents"`
		Gross        int64  `json:"gross_cents"`
		InvoiceTotal int64  `json:"invoice_total_cents"`
		Status       string `json:"status"`
		Source       string `json:"source"`
	}
	out := []line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.ID, &l.Number, &l.Contact, &l.Date, &l.Due, &l.LineNo, &l.Description, &l.ItemCode, &l.Quantity, &l.UnitPrice, &l.Discount, &l.Amount, &l.GST, &l.Gross, &l.InvoiceTotal, &l.Status, &l.Source); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 5000
	if truncated {
		out = out[:5000]
	}
	writeJSON(w, 200, map[string]any{"rows": out, "truncated": truncated})
}
