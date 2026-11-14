package main

import "net/http"

// gstAudit: GET /api/v1/reports/gst-audit?org_id&from&to — posted invoice, sales receipt, credit note and bill lines with their GST treatment.
// Sales tax report: GET /api/v1/reports/sales-tax?org_id&from&to — the same lines totalled by direction and rate.
func (a *app) gstAudit(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.gstLines(w, r, u, false)
}

func (a *app) salesTax(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.gstLines(w, r, u, true)
}

func (a *app) gstLines(w http.ResponseWriter, r *http.Request, u currentUser, summarise bool) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
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
	if summarise {
		rows, err := a.db.Query(ctx, `SELECT kind, gst_rate_bp, count(*), COALESCE(SUM(amount_cents),0), COALESCE(SUM(gst_cents),0)
			FROM (
				SELECT 'sale'::text AS kind, l.gst_rate_bp, l.amount_cents, l.gst_cents
				FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id
				WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2::date AND $3::date
				UNION ALL
				SELECT 'sale'::text, COALESCE(l.gst_rate_bp,0), l.amount_cents, COALESCE(l.gst_cents,0)
				FROM sales_receipts s CROSS JOIN LATERAL jsonb_to_recordset(s.lines) AS l(amount_cents bigint,gst_rate_bp bigint,gst_cents bigint,gst_code text)
				WHERE s.org_id=$1 AND s.receipt_date BETWEEN $2::date AND $3::date
				UNION ALL
				SELECT 'sale'::text, COALESCE(l.gst_rate_bp,0), -l.amount_cents, -COALESCE(l.gst_cents,0)
				FROM customer_credit_notes n CROSS JOIN LATERAL jsonb_to_recordset(n.lines) AS l(amount_cents bigint,gst_rate_bp bigint,gst_cents bigint,gst_code text)
				WHERE n.org_id=$1 AND n.status='open' AND n.credit_date BETWEEN $2::date AND $3::date
				UNION ALL
				SELECT 'sale'::text,
					CASE WHEN i.total_cents>0 THEN i.gst_cents*10000/i.total_cents ELSE 0 END,
					-(c.amount_cents - CASE WHEN i.total_cents>0 THEN c.amount_cents*i.gst_cents/i.total_cents ELSE 0 END),
					-(CASE WHEN i.total_cents>0 THEN c.amount_cents*i.gst_cents/i.total_cents ELSE 0 END)
				FROM credit_notes c JOIN invoices i ON i.org_id=c.org_id AND i.id=c.invoice_id
				WHERE c.org_id=$1 AND c.credit_date BETWEEN $2::date AND $3::date
				UNION ALL
				SELECT 'purchase'::text, e.gst_rate_bp, e.amount_cents-e.gst_cents, e.gst_cents
				FROM expenses e
				WHERE e.org_id=$1 AND e.expense_date BETWEEN $2::date AND $3::date AND e.gst_code <> 'NONE'
				UNION ALL
				SELECT 'purchase'::text, sc.gst_cents*10000/NULLIF(sc.total_cents,0), -sc.net_cents, -sc.gst_cents
				FROM supplier_credits sc
				WHERE sc.org_id=$1 AND sc.credit_date BETWEEN $2::date AND $3::date
				UNION ALL
				SELECT 'purchase'::text, l.gst_rate_bp, l.amount_cents, l.gst_cents
				FROM bill_lines l JOIN bills b ON b.org_id=l.org_id AND b.id=l.bill_id
				WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date BETWEEN $2::date AND $3::date
			) lines
			GROUP BY 1,2 ORDER BY 1,2`, access.ID, from, to)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		type row struct {
			Kind    string `json:"kind"`
			Rate    int64  `json:"rate_bp"`
			Lines   int64  `json:"lines"`
			Taxable int64  `json:"taxable_cents"`
			Tax     int64  `json:"tax_cents"`
		}
		out := []row{}
		var taxable, outputTax, inputTax int64
		for rows.Next() {
			var v row
			if err := rows.Scan(&v.Kind, &v.Rate, &v.Lines, &v.Taxable, &v.Tax); err != nil {
				serverError(w, err)
				return
			}
			taxable += v.Taxable
			if v.Kind == "sale" {
				outputTax += v.Tax
			} else {
				inputTax += v.Tax
			}
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		// Output and input tax stay apart: adding them was the audit's High finding (R24).
		writeJSON(w, http.StatusOK, map[string]any{"rows": out, "taxable_cents": taxable,
			"tax_cents": outputTax + inputTax, "output_tax_cents": outputTax, "input_tax_cents": inputTax,
			"net_payable_cents": outputTax - inputTax})
		return
	}
	rows, err := a.db.Query(ctx, `SELECT kind, number, date, contact, description, quantity, unit_price_cents,
		amount_cents, gst_code, gst_rate_bp, gst_cents
		FROM (
			SELECT 'sale'::text AS kind, i.number::text, i.invoice_date::text AS date, c.name AS contact,
				l.description, l.quantity, l.unit_price_cents, l.amount_cents, l.gst_code, l.gst_rate_bp, l.gst_cents,
				i.id AS doc_id, l.line_no
			FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id
			JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id
			WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'sale'::text, s.receipt_number, s.receipt_date::text, c.name,
				l.description, l.quantity, l.unit_price_cents, l.amount_cents, COALESCE(l.gst_code,'NONE'), COALESCE(l.gst_rate_bp,0), COALESCE(l.gst_cents,0),
				s.id, l.line_no
			FROM sales_receipts s JOIN contacts c ON c.org_id=s.org_id AND c.id=s.contact_id
			CROSS JOIN LATERAL jsonb_to_recordset(s.lines) AS l(line_no integer,description text,quantity bigint,unit_price_cents bigint,amount_cents bigint,gst_code text,gst_rate_bp bigint,gst_cents bigint)
			WHERE s.org_id=$1 AND s.receipt_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'sale'::text, n.credit_number, n.credit_date::text, c.name,
				l.description, l.quantity, -l.unit_price_cents, -l.amount_cents, COALESCE(l.gst_code,'NONE'), COALESCE(l.gst_rate_bp,0), -COALESCE(l.gst_cents,0),
				n.id, l.line_no
			FROM customer_credit_notes n JOIN contacts c ON c.org_id=n.org_id AND c.id=n.contact_id
			CROSS JOIN LATERAL jsonb_to_recordset(n.lines) AS l(line_no integer,description text,quantity bigint,unit_price_cents bigint,amount_cents bigint,gst_code text,gst_rate_bp bigint,gst_cents bigint)
			WHERE n.org_id=$1 AND n.status='open' AND n.credit_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'purchase'::text, COALESCE(NULLIF(b.supplier_reference,''),'Bill '||b.id::text), b.bill_date::text, c.name,
				l.description, l.quantity, l.unit_price_cents, l.amount_cents, l.gst_code, l.gst_rate_bp, l.gst_cents,
				b.id, l.line_no
			FROM bill_lines l JOIN bills b ON b.org_id=l.org_id AND b.id=l.bill_id
			JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id
			WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date BETWEEN $2::date AND $3::date
			UNION ALL
			SELECT 'purchase'::text, 'EXP-'||e.id::text, e.expense_date::text, e.payee,
				e.description, 1, e.amount_cents-e.gst_cents, e.amount_cents-e.gst_cents, e.gst_code, e.gst_rate_bp, e.gst_cents,
				e.id, 1
			FROM expenses e
			WHERE e.org_id=$1 AND e.expense_date BETWEEN $2::date AND $3::date AND e.gst_code <> 'NONE'
			UNION ALL
			SELECT 'purchase'::text, 'SC-'||sc.id::text, sc.credit_date::text, c.name,
				'Supplier credit', 1, -sc.net_cents, -sc.net_cents, 'SR',
				CASE WHEN sc.total_cents>0 THEN sc.gst_cents*10000/sc.total_cents ELSE 0 END, -sc.gst_cents,
				sc.id, 1
			FROM supplier_credits sc JOIN contacts c ON c.org_id=sc.org_id AND c.id=sc.contact_id
			WHERE sc.org_id=$1 AND sc.credit_date BETWEEN $2::date AND $3::date
		) lines
		ORDER BY date, kind, doc_id, line_no LIMIT 5001`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Kind      string `json:"kind"`
		Number    string `json:"number"`
		Date      string `json:"date"`
		Contact   string `json:"contact"`
		Desc      string `json:"description"`
		Quantity  int64  `json:"quantity"`
		UnitPrice int64  `json:"unit_price_cents"`
		Amount    int64  `json:"amount_cents"`
		GSTCode   string `json:"gst_code"`
		GSTRate   int64  `json:"gst_rate_bp"`
		GST       int64  `json:"gst_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.Kind, &v.Number, &v.Date, &v.Contact, &v.Desc, &v.Quantity, &v.UnitPrice, &v.Amount, &v.GSTCode, &v.GSTRate, &v.GST); err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "truncated": truncated})
}
