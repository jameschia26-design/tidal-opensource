package main

import (
	"net/http"
	"strings"

	"tidal/backend/internal/taxrules"
)

// convertExpenseToInvoice turns one billable expense into a draft customer invoice line.
// Nothing posts here: the invoice is a draft until the user posts it through the normal invoice flow.
// ponytail: one expense makes at most one invoice; splitting an expense across invoices is not supported.
func (a *app) convertExpenseToInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID   int64  `json:"org_id"`
		GSTCode string `json:"gst_code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var (
		description, date string
		customerID        int64
		amount            int64
		billable          bool
	)
	err = tx.QueryRow(ctx, `SELECT description,expense_date::text,COALESCE(customer_id,0),amount_cents,billable FROM expenses WHERE org_id=$1 AND id=$2`, access.ID, id).
		Scan(&description, &date, &customerID, &amount, &billable)
	if err != nil {
		if err.Error() == "no rows in result set" {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	if !billable || customerID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "expense_not_billable")
		return
	}
	var linked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM expense_invoice_links WHERE org_id=$1 AND expense_id=$2)`, access.ID, id).Scan(&linked); err != nil {
		serverError(w, err)
		return
	}
	if linked {
		writeError(w, http.StatusConflict, "expense_already_invoiced")
		return
	}
	gstRegistered, err := gstRegisteredOn(ctx, tx, access.ID, date)
	if err != nil {
		serverError(w, err)
		return
	}
	// Expense amount is the tax-exclusive invoice price; GST is never copied from the expense.
	invoiceDate, validDate := parseDay(date)
	if !validDate {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	lines, net, gst, code := buildSalesLines([]invoiceLineIn{{Description: strings.TrimSpace(description), Quantity: 1, UnitPriceCents: amount, GSTCode: in.GSTCode}}, gstRegistered, taxrules.GSTRateBP(invoiceDate), false)
	if code != "" {
		status := http.StatusBadRequest
		if code == "unsupported_gst" {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, code)
		return
	}
	line := lines[0]
	var invID int64
	if err := tx.QueryRow(ctx, `INSERT INTO invoices(org_id,contact_id,invoice_date,due_date,total_cents,created_by,reference,notes,terms,commercial,gst_cents) VALUES($1,$2,$3,$3,$4,$5,'','Billable expense','','{}',$6) RETURNING id`,
		access.ID, customerID, date, net+gst, u.ID, gst).Scan(&invID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents,gst_code,gst_rate_bp,gst_cents) VALUES($1,$2,1,$3,1,$4,$5,$6,$7,$8)`, access.ID, invID, line.Description, line.UnitPriceCents, line.AmountCents, line.GSTCode, line.GSTRateBP, line.GSTCents); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO expense_invoice_links(org_id,expense_id,invoice_id) VALUES($1,$2,$3)`, access.ID, id, invID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'expense.invoiced',jsonb_build_object('expense_id',$3::bigint,'invoice_id',$4::bigint))`, access.ID, u.ID, id, invID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invoice_id": invID})
}
