package main

import (
	"net/http"
	"strconv"
)

// documentReport lists posted documents dated in the period, with balances as at its end.
// ponytail: document summaries only; add line detail, other date searches and export for Xero parity.
func (a *app) documentReport(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	// Expected date is the invoice due date, the date payment is expected. Invoice sent is the posting
	// date, the only send marker stored. Source comes from the stored repeating/subscription links.
	sql := `SELECT i.id, i.number::text, c.name, i.invoice_date::text, i.due_date::text, i.total_cents,
 i.total_cents-COALESCE((SELECT sum(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id AND p.payment_date<=$3),0)-COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id AND n.credit_date<=$3),0)-COALESCE((SELECT sum(ca.amount_cents) FROM customer_credit_applications ca WHERE ca.org_id=i.org_id AND ca.invoice_id=i.id AND ca.applied_on<=$3),0),
 i.status,''::text,i.due_date::text,COALESCE(to_char(i.posted_at,'YYYY-MM-DD'),''),CASE WHEN i.subscription_id IS NOT NULL THEN 'Subscription invoice' WHEN i.recurring_template_id IS NOT NULL THEN 'Repeating invoice' ELSE 'Invoice' END
 FROM invoices i JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2 AND $3 AND ($4::bigint IS NULL OR i.contact_id=$4) ORDER BY i.invoice_date,i.id LIMIT 5001`
	if kind == "payable" {
		sql = `SELECT b.id,b.supplier_reference,c.name,b.bill_date::text,b.due_date::text,b.total_cents,
 b.total_cents-COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$3),0),
 b.status,COALESCE(b.planned_date::text,''),''::text,''::text,CASE WHEN b.recurring_profile_id IS NOT NULL THEN 'Repeating bill' ELSE 'Bill' END
 FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date BETWEEN $2 AND $3 AND ($4::bigint IS NULL OR b.contact_id=$4) ORDER BY b.bill_date,b.id LIMIT 5001`
	}
	rows, err := a.db.Query(r.Context(), sql, access.ID, from, to, contact)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID           int64  `json:"id"`
		Number       string `json:"number"`
		Contact      string `json:"contact"`
		Date         string `json:"date"`
		Due          string `json:"due_date"`
		Total        int64  `json:"total_cents"`
		Balance      int64  `json:"balance_cents"`
		Status       string `json:"status"`
		PlannedDate  string `json:"planned_date,omitempty"`
		ExpectedDate string `json:"expected_date,omitempty"`
		SentDate     string `json:"sent_date,omitempty"`
		Source       string `json:"source"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ID, &v.Number, &v.Contact, &v.Date, &v.Due, &v.Total, &v.Balance, &v.Status, &v.PlannedDate, &v.ExpectedDate, &v.SentDate, &v.Source); err != nil {
			serverError(w, err)
			return
		}
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
	writeJSON(w, 200, map[string]any{"rows": out, "truncated": truncated})
}
