package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// applyCustomerCredit applies part or all of an open customer credit note to a posted invoice of the same customer.
// Allocation only: the credit already reduced receivables when opened, so no journal entry is posted here.
// ponytail: one application per call; no reversal. Reverse by issuing a new credit note instead.
func (a *app) applyCustomerCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	creditID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID       int64  `json:"org_id"`
		InvoiceID   int64  `json:"invoice_id"`
		AmountCents int64  `json:"amount_cents"`
		AppliedOn   string `json:"applied_on"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	date, ok := parseDay(in.AppliedOn)
	if !ok || in.InvoiceID <= 0 || in.AmountCents < 1 || in.AmountCents > maxMoneyCents {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var creditStatus string
	var creditContact, creditTotal int64
	var creditDate time.Time
	err = tx.QueryRow(ctx, `SELECT status,contact_id,total_cents,credit_date FROM customer_credit_notes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, creditID).
		Scan(&creditStatus, &creditContact, &creditTotal, &creditDate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if creditStatus != "open" {
		writeError(w, http.StatusUnprocessableEntity, "credit_not_open")
		return
	}
	var invStatus string
	var invContact, invTotal int64
	var invDate time.Time
	err = tx.QueryRow(ctx, `SELECT status,contact_id,total_cents,invoice_date FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, in.InvoiceID).
		Scan(&invStatus, &invContact, &invTotal, &invDate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if invStatus != "posted" {
		writeError(w, http.StatusUnprocessableEntity, "invoice_not_posted")
		return
	}
	if invContact != creditContact {
		writeError(w, http.StatusUnprocessableEntity, "invalid_contact")
		return
	}
	if date.Before(creditDate) || date.Before(invDate) {
		writeError(w, http.StatusUnprocessableEntity, "applied_before_dated")
		return
	}
	// Credit still open after earlier applications.
	var appliedCredit int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND credit_note_id=$2),0) + COALESCE((SELECT sum(amount_cents) FROM customer_credit_refunds WHERE org_id=$1 AND credit_note_id=$2),0)`, access.ID, creditID).Scan(&appliedCredit); err != nil {
		serverError(w, err)
		return
	}
	if appliedCredit+in.AmountCents > creditTotal {
		writeError(w, http.StatusUnprocessableEntity, "credit_exceeds_balance")
		return
	}
	// Invoice still owed after payments, direct credit notes and earlier applications.
	var paid, creditedDirect, appliedToInvoice int64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(amount_cents) FROM payments WHERE org_id=$1 AND invoice_id=$2),0),
		COALESCE((SELECT sum(amount_cents) FROM credit_notes WHERE org_id=$1 AND invoice_id=$2),0),
		COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND invoice_id=$2),0)`,
		access.ID, in.InvoiceID).Scan(&paid, &creditedDirect, &appliedToInvoice); err != nil {
		serverError(w, err)
		return
	}
	owed := invTotal - paid - creditedDirect - appliedToInvoice
	if in.AmountCents > owed {
		writeError(w, http.StatusUnprocessableEntity, "application_exceeds_balance")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO customer_credit_applications(org_id,credit_note_id,invoice_id,amount_cents,applied_on,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		access.ID, creditID, in.InvoiceID, in.AmountCents, date, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'credit_note.applied',jsonb_build_object('application_id',$3::bigint,'credit_note_id',$4::bigint,'invoice_id',$5::bigint))`, access.ID, u.ID, id, creditID, in.InvoiceID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "amount_cents": in.AmountCents, "invoice_balance_cents": owed - in.AmountCents})
}
