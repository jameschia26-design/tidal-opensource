package main

import (
	"net/http"
	"time"
)

// journalReport: GET /api/v1/reports/journals?org_id&from&to — every posted journal line with its document source.
// A journal's idempotency key names what posted it (invoice-post:12, payment:..., manual:...), so the report can
// label the source without joining each document type.
// The order-by is chosen by the caller from a fixed list, never interpolated from raw input.
// ponytail: no reversal marker or column chooser yet; add the columns when the screen grows them.
func (a *app) journalReport(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	orderBy := "j.entry_date,j.id,jl.line_no"
	switch r.URL.Query().Get("order") {
	case "", "date_asc":
	case "date_desc":
		orderBy = "j.entry_date DESC,j.id DESC,jl.line_no"
	case "id_asc":
		orderBy = "j.id,jl.line_no"
	case "id_desc":
		orderBy = "j.id DESC,jl.line_no"
	default:
		writeError(w, 400, "invalid_input")
		return
	}
	type row struct {
		JournalID  int64  `json:"journal_id"`
		Date       string `json:"entry_date"`
		PostedDate string `json:"posted_date"`
		PostedBy   string `json:"posted_by"`
		Memo       string `json:"memo"`
		Source     string `json:"source"`
		LineNo     int    `json:"line_no"`
		Account    string `json:"account"`
		Code       string `json:"code"`
		Debit      int64  `json:"debit_cents"`
		Credit     int64  `json:"credit_cents"`
	}
	rows, err := a.db.Query(r.Context(), `SELECT j.id,j.entry_date,j.created_at,COALESCE(u.email,''),j.memo,j.idempotency_key,jl.line_no,a.code,a.name,jl.debit_cents,jl.credit_cents
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		LEFT JOIN users u ON u.id=j.created_by
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date
		ORDER BY `+orderBy+` LIMIT 5000`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []row{}
	var debit, credit int64
	for rows.Next() {
		var v row
		var entry, posted time.Time
		if err := rows.Scan(&v.JournalID, &entry, &posted, &v.PostedBy, &v.Memo, &v.Source, &v.LineNo, &v.Code, &v.Account, &v.Debit, &v.Credit); err != nil {
			serverError(w, err)
			return
		}
		v.Date = day(entry)
		v.PostedDate = day(posted)
		debit += v.Debit
		credit += v.Credit
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": out,
		"debit_cents": debit, "credit_cents": credit, "truncated": len(out) == 5000})
}

// cashValidation: GET /api/v1/reports/cash-validation?org_id&as_of — per customer, invoiced against cash and credits.
// A balance other than zero means the customer's documents and its settlements disagree; posting guards make that
// impossible today, so the report is a standing check rather than a fixer.
func (a *app) cashValidation(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf := day(time.Now())
	if raw := r.URL.Query().Get("as_of"); raw != "" {
		if _, okDay := parseDay(raw); !okDay {
			writeError(w, 400, "invalid_input")
			return
		}
		asOf = raw
	}
	if _, okDay := parseDay(asOf); !okDay {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	type row struct {
		ContactID int64  `json:"contact_id"`
		Customer  string `json:"customer"`
		Invoiced  int64  `json:"invoiced_cents"`
		Paid      int64  `json:"paid_cents"`
		Credits   int64  `json:"credits_cents"`
		Balance   int64  `json:"balance_cents"`
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id,c.name,
		COALESCE(SUM(i.total_cents),0)::bigint,
		COALESCE((SELECT SUM(p.amount_cents) FROM payments p JOIN invoices pi ON pi.org_id=p.org_id AND pi.id=p.invoice_id
			WHERE pi.org_id=c.org_id AND pi.contact_id=c.id AND p.payment_date<=$2::date),0)::bigint,
		COALESCE((SELECT SUM(n.amount_cents) FROM credit_notes n JOIN invoices ci ON ci.org_id=n.org_id AND ci.id=n.invoice_id
			WHERE ci.org_id=c.org_id AND ci.contact_id=c.id AND n.credit_date<=$2::date),0)::bigint
		FROM contacts c JOIN invoices i ON i.org_id=c.org_id AND i.contact_id=c.id AND i.status='posted' AND i.invoice_date<=$2::date
		WHERE c.org_id=$1 AND c.kind IN ('customer','both')
		GROUP BY c.id ORDER BY c.name,c.id LIMIT 500`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []row{}
	var invoiced, paid, credits int64
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ContactID, &v.Customer, &v.Invoiced, &v.Paid, &v.Credits); err != nil {
			serverError(w, err)
			return
		}
		v.Balance = v.Invoiced - v.Paid - v.Credits
		invoiced += v.Invoiced
		paid += v.Paid
		credits += v.Credits
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"as_of": asOf, "rows": out,
		"invoiced_cents": invoiced, "paid_cents": paid, "credits_cents": credits,
		"balance_cents": invoiced - paid - credits})
}
