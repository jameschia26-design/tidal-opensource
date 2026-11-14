package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reversing a posted document writes a mirrored journal instead of editing history (R15). A reversal that
// would land in a closed period is dated the first open day, so closed months stay closed.

// reversalDateFor moves a requested reversal date past any closed period.
func reversalDateFor(ctx context.Context, q rowQueryer, orgID int64, requested time.Time) (time.Time, error) {
	var closed *time.Time
	if err := q.QueryRow(ctx, `SELECT closed_through FROM period_closes WHERE org_id=$1`, orgID).Scan(&closed); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return requested, err
	}
	if closed != nil && !requested.After(*closed) {
		return closed.AddDate(0, 0, 1), nil
	}
	return requested, nil
}

// alreadyReversed reports whether a document already has a reversal on file.
func alreadyReversed(ctx context.Context, q rowQueryer, orgID int64, kind string, docID int64) (bool, error) {
	var yes bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM document_reversals WHERE org_id=$1 AND doc_kind=$2 AND doc_id=$3)`, orgID, kind, docID).Scan(&yes)
	return yes, err
}

// postMirroredJournal copies a journal's lines with debits and credits swapped.
func postMirroredJournal(ctx context.Context, tx pgx.Tx, orgID, userID, original int64, date time.Time, memo, key string) (int64, error) {
	hash := sha256.Sum256([]byte(key))
	var jid int64
	if err := tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by,reversal_of)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, orgID, date, memo, key, hash[:], userID, original).Scan(&jid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description)
		SELECT org_id,$2,line_no,account_id,credit_cents,debit_cents,description FROM journal_lines WHERE org_id=$1 AND journal_id=$3`,
		orgID, jid, original); err != nil {
		return 0, err
	}
	return jid, nil
}

// recordReversal stores the link and refuses a second reversal of the same document.
func recordReversal(ctx context.Context, tx pgx.Tx, orgID, userID int64, kind string, docID, original, reversal int64, date time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO document_reversals(org_id,doc_kind,doc_id,original_journal_id,reversal_journal_id,reversal_date,created_by)
		VALUES($1,$2,$3,$4,$5,$6::date,$7)`, orgID, kind, docID, original, reversal, day(date), userID); err != nil {
		return err
	}
	// The entity columns are what the history filter reads; the detail keeps the journal link.
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail,entity_kind,entity_id)
		VALUES($1,$2,$3,jsonb_build_object('reversal_journal_id',$4::bigint),$5,$6)`,
		orgID, userID, kind+".reversed", reversal, kind, docID)
	return err
}

type reversalInput struct {
	OrgID        int64  `json:"org_id"`
	ReversalDate string `json:"reversal_date"`
}

// voidBill: POST /api/v1/bills/{id}/void — voids a posted bill that has no payments or credits.
// The old /reverse route calls the same handler so both links behave identically.
func (a *app) voidBill(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	if err := tx.QueryRow(ctx, `SELECT status, journal_id FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&status, &jid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if status != "posted" || jid == nil {
		writeError(w, http.StatusConflict, "bill_not_posted")
		return
	}
	if done, err := alreadyReversed(ctx, tx, access.ID, "bill", id); err != nil {
		serverError(w, err)
		return
	} else if done {
		writeError(w, http.StatusConflict, "already_reversed")
		return
	}
	var settled int
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM bill_payments WHERE org_id=$1 AND bill_id=$2 AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=bill_payments.org_id AND r.doc_kind='bill_payment' AND r.doc_id=bill_payments.id))
		+ (SELECT count(*) FROM supplier_credits WHERE org_id=$1 AND bill_id=$2)`, access.ID, id).Scan(&settled); err != nil {
		serverError(w, err)
		return
	}
	if settled > 0 {
		writeError(w, http.StatusConflict, "bill_has_payments_or_credits")
		return
	}
	date, err := reversalDateFor(ctx, tx, access.ID, defaultDay(in.ReversalDate))
	if err != nil {
		serverError(w, err)
		return
	}
	rev, err := postMirroredJournal(ctx, tx, access.ID, u.ID, *jid, date, "Reversal of bill "+strconv.FormatInt(id, 10), "reverse-bill:"+strconv.FormatInt(id, 10))
	if err != nil {
		serverError(w, err)
		return
	}
	if err := recordReversal(ctx, tx, access.ID, u.ID, "bill", id, *jid, rev, date); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(w, http.StatusConflict, "already_reversed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE bills SET status='voided' WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "voided", "lifecycle_status": "voided", "journal_id": rev, "reversal_date": day(date)})
}

// reverseBillPayment: POST /api/v1/bill-payments/{id}/reverse
func (a *app) reverseBillPayment(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.reverseDocument(w, r, u, "bill_payment", "bill", `SELECT bp.bill_id,bp.journal_id FROM bill_payments bp WHERE bp.org_id=$1 AND bp.id=$2`)
}

// reverseExpense: POST /api/v1/expenses/{id}/reverse
func (a *app) reverseExpense(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.reverseDocument(w, r, u, "expense", "", `SELECT e.id,e.journal_id FROM expenses e WHERE e.org_id=$1 AND e.id=$2`)
}

// reverseDocument reverses a payment or expense whose only state is its journal.
func (a *app) reverseDocument(w http.ResponseWriter, r *http.Request, u currentUser, kind, bankKind, lookup string) {
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
	var docRef, jid int64
	if err := tx.QueryRow(ctx, lookup+` FOR UPDATE`, access.ID, id).Scan(&docRef, &jid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if done, err := alreadyReversed(ctx, tx, access.ID, kind, id); err != nil {
		serverError(w, err)
		return
	} else if done {
		writeError(w, http.StatusConflict, "already_reversed")
		return
	}
	// A record still matched to a bank line must be unmatched first, so the statement stays explained.
	var matched bool
	if bankKind != "" {
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bank_statement_lines WHERE org_id=$1 AND matched_payment_kind=$2 AND matched_payment_id=$3)`,
			access.ID, bankKind, id).Scan(&matched); err != nil {
			serverError(w, err)
			return
		}
	}
	if matched {
		writeError(w, http.StatusConflict, "unmatch_bank_line_first")
		return
	}
	date, err := reversalDateFor(ctx, tx, access.ID, defaultDay(in.ReversalDate))
	if err != nil {
		serverError(w, err)
		return
	}
	rev, err := postMirroredJournal(ctx, tx, access.ID, u.ID, jid, date, "Reversal of "+kind+" "+strconv.FormatInt(id, 10), "reverse-"+kind+":"+strconv.FormatInt(id, 10))
	if err != nil {
		serverError(w, err)
		return
	}
	if err := recordReversal(ctx, tx, access.ID, u.ID, kind, id, jid, rev, date); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			writeError(w, http.StatusConflict, "already_reversed")
			return
		}
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "journal_id": rev, "reversal_date": day(date)})
}

// defaultDay parses an optional date, falling back to today (Singapore date, as the app uses).
func defaultDay(s string) time.Time {
	if d, ok := parseDay(s); ok {
		return d
	}
	return time.Now().UTC()
}
