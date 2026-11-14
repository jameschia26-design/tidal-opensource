package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// lifecycleCase maps the stored status onto the user-facing lifecycle. Paid is derived from the
// balance, never typed in; a part-paid or over-paid posted document is still awaiting payment.
const lifecycleCase = `CASE WHEN d.status = 'posted' AND d.balance_cents <= 0 THEN 'paid'
  WHEN d.status = 'posted' THEN 'awaiting_payment' ELSE d.status END AS lifecycle`

func validLifecycle(status string) bool {
	switch status {
	case "", "draft", "awaiting_approval", "posted", "awaiting_payment", "paid", "voided":
		return true
	}
	return false
}

func lifecycleStatus(status string, balance int64) string {
	if status == "posted" {
		if balance <= 0 {
			return "paid"
		}
		return "awaiting_payment"
	}
	return status
}

// lifecycleCounts returns a count for every filter the tabs offer, including statuses with no rows.
func lifecycleCounts(ctx context.Context, q rowQueryer, base string, orgID int64) (map[string]int, error) {
	counts := map[string]int{"all": 0, "draft": 0, "awaiting_approval": 0, "awaiting_payment": 0, "paid": 0, "voided": 0}
	rows, err := q.Query(ctx, `SELECT lifecycle, count(*) FROM (`+base+`) x GROUP BY lifecycle`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
		counts["all"] += n
	}
	return counts, rows.Err()
}

// voidInvoice: POST /api/v1/invoices/{id}/void. A posted, unallocated invoice gets a dated
// mirrored journal and becomes voided. Paid or part-paid invoices must be corrected first.
func (a *app) voidInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in reversalInput
	if !decodeJSON(w, r, &in) {
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
	var status string
	var jid *int64
	if err := tx.QueryRow(ctx, `SELECT status, journal_id FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&status, &jid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if status == "voided" {
		writeError(w, http.StatusConflict, "already_voided")
		return
	}
	if status != "posted" || jid == nil {
		writeError(w, http.StatusConflict, "invoice_not_posted")
		return
	}
	if done, err := alreadyReversed(ctx, tx, access.ID, "invoice", id); err != nil {
		serverError(w, err)
		return
	} else if done {
		writeError(w, http.StatusConflict, "already_reversed")
		return
	}
	var allocated int64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(amount_cents) FROM payments WHERE org_id=$1 AND invoice_id=$2), 0)
		+ COALESCE((SELECT sum(amount_cents) FROM credit_notes WHERE org_id=$1 AND invoice_id=$2), 0)
		+ COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND invoice_id=$2), 0)`, access.ID, id).Scan(&allocated); err != nil {
		serverError(w, err)
		return
	}
	if allocated > 0 {
		writeError(w, http.StatusConflict, "invoice_has_allocations")
		return
	}
	date, err := reversalDateFor(ctx, tx, access.ID, defaultDay(in.ReversalDate))
	if err != nil {
		serverError(w, err)
		return
	}
	rev, err := postMirroredJournal(ctx, tx, access.ID, u.ID, *jid, date, "Void of invoice "+strconv.FormatInt(id, 10), "void-invoice:"+strconv.FormatInt(id, 10))
	if err != nil {
		serverError(w, err)
		return
	}
	if err := recordReversal(ctx, tx, access.ID, u.ID, "invoice", id, *jid, rev, date); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "already_reversed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE invoices SET status='voided' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "voided", "lifecycle_status": "voided", "journal_id": rev, "reversal_date": day(date)})
}

// voidCustomerCredit: POST /api/v1/credit-notes/{id}/void. An open credit note that has not been
// applied or refunded is voided with a mirrored journal; the amount credited disappears from the books.
func (a *app) voidCustomerCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in reversalInput
	if !decodeJSON(w, r, &in) {
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
	var status string
	var jid *int64
	if err := tx.QueryRow(ctx, `SELECT status, journal_id FROM customer_credit_notes WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&status, &jid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if status == "void" {
		writeError(w, http.StatusConflict, "already_voided")
		return
	}
	if status != "open" || jid == nil {
		writeError(w, http.StatusConflict, "credit_note_not_open")
		return
	}
	if done, err := alreadyReversed(ctx, tx, access.ID, "customer_credit_note", id); err != nil {
		serverError(w, err)
		return
	} else if done {
		writeError(w, http.StatusConflict, "already_reversed")
		return
	}
	var allocated int64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND credit_note_id=$2), 0)
		+ COALESCE((SELECT sum(amount_cents) FROM customer_credit_refunds WHERE org_id=$1 AND credit_note_id=$2), 0)`, access.ID, id).Scan(&allocated); err != nil {
		serverError(w, err)
		return
	}
	if allocated > 0 {
		writeError(w, http.StatusConflict, "credit_note_has_allocations")
		return
	}
	date, err := reversalDateFor(ctx, tx, access.ID, defaultDay(in.ReversalDate))
	if err != nil {
		serverError(w, err)
		return
	}
	rev, err := postMirroredJournal(ctx, tx, access.ID, u.ID, *jid, date, "Void of credit note "+strconv.FormatInt(id, 10), "void-customer-credit:"+strconv.FormatInt(id, 10))
	if err != nil {
		serverError(w, err)
		return
	}
	if err := recordReversal(ctx, tx, access.ID, u.ID, "customer_credit_note", id, *jid, rev, date); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "already_reversed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE customer_credit_notes SET status='void' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "void", "lifecycle_status": "voided", "journal_id": rev, "reversal_date": day(date)})
}

// voidJournal: POST /api/v1/journals/{id}/void. A manual journal is voided by a dated mirrored
// journal and stays in the history as voided; it cannot be voided twice or un-voided.
func (a *app) voidJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in reversalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
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
	var originalDate time.Time
	var key string
	var reversalOf *int64
	if err := tx.QueryRow(ctx, `SELECT entry_date,idempotency_key,reversal_of FROM journals WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&originalDate, &key, &reversalOf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if reversalOf != nil || !strings.HasPrefix(key, "manual:") {
		writeError(w, http.StatusConflict, "document_reversal_required")
		return
	}
	if done, err := alreadyReversed(ctx, tx, access.ID, "journal", id); err != nil {
		serverError(w, err)
		return
	} else if done {
		writeError(w, http.StatusConflict, "already_voided")
		return
	}
	var existing int64
	err = tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND reversal_of=$2`, access.ID, id).Scan(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "already_voided")
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	date, err := reversalDateFor(ctx, tx, access.ID, defaultDay(in.ReversalDate))
	if err != nil {
		serverError(w, err)
		return
	}
	if date.Before(originalDate) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	rev, err := postMirroredJournal(ctx, tx, access.ID, u.ID, id, date, "Void of journal "+strconv.FormatInt(id, 10), "void-journal:"+strconv.FormatInt(id, 10))
	if err != nil {
		serverError(w, err)
		return
	}
	if err := recordReversal(ctx, tx, access.ID, u.ID, "journal", id, id, rev, date); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "already_voided")
			return
		}
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "voided", "lifecycle_status": "voided", "journal_id": rev, "reversal_date": day(date)})
}

// submitBill: POST /api/v1/bills/{id}/submit moves a draft to awaiting approval. Approving is the
// ordinary post route, so approval and posting stay one accounting event.
func (a *app) submitBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in reversalInput
	if !decodeJSON(w, r, &in) {
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
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if status == "awaiting_approval" {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status, "lifecycle_status": status})
		return
	}
	if status != "draft" {
		writeError(w, http.StatusConflict, "bill_not_draft")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE bills SET status='awaiting_approval' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bill.submitted',jsonb_build_object('bill_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "awaiting_approval", "lifecycle_status": "awaiting_approval"})
}

// submitInvoice: POST /api/v1/invoices/{id}/submit moves a draft to awaiting approval. The ordinary
// post route approves it, so approval is the accounting event rather than a separate status change.
func (a *app) submitInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in reversalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var status string
	if err := a.db.QueryRow(ctx, `SELECT status FROM invoices WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if status == "awaiting_approval" {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status, "lifecycle_status": status})
		return
	}
	if status != "draft" {
		writeError(w, http.StatusConflict, "invoice_not_draft")
		return
	}
	if _, err := a.db.Exec(ctx, `UPDATE invoices SET status='awaiting_approval' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'invoice.submitted',jsonb_build_object('invoice_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "awaiting_approval", "lifecycle_status": "awaiting_approval"})
}
