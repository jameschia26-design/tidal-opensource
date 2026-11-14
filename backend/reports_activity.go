package main

import (
	"net/http"
	"strconv"
)

// documentActivity: GET /api/v1/reports/document-activity?org_id&kind=receivable|payable&from&to[&contact_id]
// Lists the movements behind customer invoices or supplier bills inside the period: documents, payments,
// credit notes, applied credits and refunds. Amounts are positive for the document side and negative for
// money or credit applied against it.
// Supplier bill rows also carry the bill line description, the journal that posted the movement and the
// running balance for the contact. That running balance counts every movement in the contact's history,
// so it starts from the first bill ever posted, not from the report's from date.
// ponytail: one row per movement, no running balance on the customer side and no grouping by contact; add
// when the statement view needs it.
func (a *app) documentActivity(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	if v := q.Get("contact_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, 400, "invalid_input")
			return
		}
		contact = &n
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	sql := `SELECT m.dd::text, m.kind, m.reference, m.contact, m.amount_cents FROM (
		SELECT i.invoice_date dd, 'Invoice' kind, i.number::text reference, c.name contact, i.total_cents amount_cents
		FROM invoices i JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id
		WHERE i.org_id=$1 AND i.status='posted' AND ($4::bigint IS NULL OR i.contact_id=$4)
		UNION ALL
		SELECT p.payment_date AS dd, 'Payment', i.number::text, c.name, -p.amount_cents
		FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id
		JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id
		WHERE p.org_id=$1 AND ($4::bigint IS NULL OR i.contact_id=$4)
		UNION ALL
		SELECT n.credit_date AS dd, 'Credit note', i.number::text, c.name, -n.amount_cents
		FROM credit_notes n JOIN invoices i ON i.org_id=n.org_id AND i.id=n.invoice_id
		JOIN contacts c ON c.org_id=n.org_id AND c.id=n.contact_id
		WHERE n.org_id=$1 AND ($4::bigint IS NULL OR n.contact_id=$4)
		UNION ALL
		SELECT ca.applied_on AS dd, 'Credit applied', i.number::text, c.name, -ca.amount_cents
		FROM customer_credit_applications ca JOIN invoices i ON i.org_id=ca.org_id AND i.id=ca.invoice_id
		JOIN contacts c ON c.org_id=ca.org_id AND c.id=i.contact_id
		WHERE ca.org_id=$1 AND ($4::bigint IS NULL OR i.contact_id=$4)
		UNION ALL
		SELECT r.refund_date AS dd, 'Refund', cn.credit_number, c.name, -r.amount_cents
		FROM customer_credit_refunds r JOIN customer_credit_notes cn ON cn.org_id=r.org_id AND cn.id=r.credit_note_id
		JOIN contacts c ON c.org_id=cn.org_id AND c.id=cn.contact_id
		WHERE r.org_id=$1 AND ($4::bigint IS NULL OR cn.contact_id=$4)) m
		WHERE m.dd BETWEEN $2::date AND $3::date ORDER BY m.dd, m.kind, m.reference LIMIT 5001`
	if kind == "payable" {
		sql = `SELECT m.dd::text, m.kind, m.reference, m.description, m.source, m.contact, m.amount_cents,
			SUM(m.amount_cents) OVER (PARTITION BY m.contact ORDER BY m.dd, m.kind, m.reference)
			FROM (
			SELECT b.bill_date dd, 'Bill' kind, COALESCE(NULLIF(b.supplier_reference,''), 'Bill '||b.id::text) reference,
				COALESCE((SELECT string_agg(DISTINCT bl.description, '; ') FROM bill_lines bl WHERE bl.org_id=b.org_id AND bl.bill_id=b.id),'') description,
				'Journal '||b.journal_id::text source, c.name contact, b.total_cents amount_cents
			FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id
			WHERE b.org_id=$1 AND b.status='posted' AND ($4::bigint IS NULL OR b.contact_id=$4)
			UNION ALL
			SELECT p.payment_date AS dd, 'Payment', COALESCE(NULLIF(b.supplier_reference,''), 'Bill '||b.id::text),
				COALESCE((SELECT string_agg(DISTINCT bl.description, '; ') FROM bill_lines bl WHERE bl.org_id=b.org_id AND bl.bill_id=b.id),''),
				'Journal '||p.journal_id::text, c.name, -p.amount_cents
			FROM bill_payments p JOIN bills b ON b.org_id=p.org_id AND b.id=p.bill_id
			JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id
			WHERE p.org_id=$1 AND ($4::bigint IS NULL OR b.contact_id=$4)) m
			WHERE m.dd BETWEEN $2::date AND $3::date ORDER BY m.dd, m.kind, m.reference LIMIT 5001`
	}
	rows, err := a.db.Query(r.Context(), sql, access.ID, from, to, contact)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Date        string `json:"date"`
		Kind        string `json:"kind"`
		Reference   string `json:"reference"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Contact     string `json:"contact"`
		Amount      int64  `json:"amount_cents"`
		Balance     int64  `json:"balance_cents"`
	}
	out := []row{}
	var net int64
	for rows.Next() {
		var v row
		if kind == "payable" {
			if err := rows.Scan(&v.Date, &v.Kind, &v.Reference, &v.Description, &v.Source, &v.Contact, &v.Amount, &v.Balance); err != nil {
				serverError(w, err)
				return
			}
		} else if err := rows.Scan(&v.Date, &v.Kind, &v.Reference, &v.Contact, &v.Amount); err != nil {
			serverError(w, err)
			return
		}
		net += v.Amount
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 5000
	if truncated {
		out = out[:5000]
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "net_cents": net, "truncated": truncated})
}
