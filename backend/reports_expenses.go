package main

import "net/http"

// claimDetail: GET /api/v1/reports/expense-claim-detail?org_id&from&to — spend money rows by date, payee and description.
// ponytail: no claiming or approval state (expenses are recorded as paid), no per-user, receipt or column chooser.
func (a *app) claimDetail(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT e.expense_date::text,e.payee,e.description,a.code,a.name,e.amount_cents,
		EXISTS(SELECT 1 FROM expense_invoice_links l WHERE l.org_id=e.org_id AND l.expense_id=e.id),
		e.billable
		FROM expenses e JOIN accounts a ON a.org_id=e.org_id AND a.id=e.expense_account_id
		WHERE e.org_id=$1 AND e.expense_date BETWEEN $2::date AND $3::date ORDER BY e.expense_date,e.id LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Date        string `json:"date"`
		Payee       string `json:"payee"`
		Description string `json:"description"`
		Code        string `json:"account_code"`
		Account     string `json:"account"`
		Amount      int64  `json:"amount_cents"`
		Invoiced    bool   `json:"invoiced"`
		Billable    bool   `json:"billable"`
	}
	out := []row{}
	var total int64
	for rows.Next() {
		var x row
		var invoice bool
		if err := rows.Scan(&x.Date, &x.Payee, &x.Description, &x.Code, &x.Account, &x.Amount, &invoice, &x.Billable); err != nil {
			serverError(w, err)
			return
		}
		x.Invoiced = invoice
		total += x.Amount
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 500
	if truncated {
		out = out[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "total_cents": total, "truncated": truncated})
}

// billableOutstanding: GET /api/v1/reports/billable-expenses-outstanding?org_id — billable spend rows not yet attached to an invoice.
// ponytail: invoiced means attached through the expense-invoice link; conversion state is not tracked.
func (a *app) billableOutstanding(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT e.expense_date::text,e.payee,e.description,a.code,a.name,e.amount_cents,c.name
		FROM expenses e JOIN accounts a ON a.org_id=e.org_id AND a.id=e.expense_account_id
		LEFT JOIN contacts c ON c.org_id=e.org_id AND c.id=e.customer_id
		WHERE e.org_id=$1 AND e.billable AND NOT EXISTS (SELECT 1 FROM expense_invoice_links l WHERE l.org_id=e.org_id AND l.expense_id=e.id)
		ORDER BY e.expense_date,e.id LIMIT 501`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Date     string  `json:"date"`
		Payee    string  `json:"payee"`
		Desc     string  `json:"description"`
		Code     string  `json:"account_code"`
		Account  string  `json:"account"`
		Amount   int64   `json:"amount_cents"`
		Customer *string `json:"customer"`
	}
	out := []row{}
	var total int64
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.Date, &x.Payee, &x.Desc, &x.Code, &x.Account, &x.Amount, &x.Customer); err != nil {
			serverError(w, err)
			return
		}
		total += x.Amount
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 500
	if truncated {
		out = out[:500]
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "total_cents": total, "truncated": truncated})
}
