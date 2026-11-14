package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// refundCustomerCredit pays money back to the customer against an open credit note.
// Posts Dr Accounts Receivable, Cr Bank: the credit already reduced receivables when opened.
// ponytail: refund must stay within the credit's unused balance; no reversal endpoint. Add a reversal when needed.
func (a *app) refundCustomerCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	creditID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID          int64  `json:"org_id"`
		AmountCents    int64  `json:"amount_cents"`
		RefundDate     string `json:"refund_date"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	date, okDate := parseDay(in.RefundDate)
	key := strings.TrimSpace(in.IdempotencyKey)
	if in.AmountCents <= 0 || in.AmountCents > maxMoneyCents || !okDate || key == "" || len(key) > 90 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	hash := sha256.Sum256([]byte(fmt.Sprintf("credit-refund|%d|%d|%s", creditID, in.AmountCents, day(date))))
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

	// Same key again with the same request returns the first result.
	var rid, rjid int64
	var rhash []byte
	err = tx.QueryRow(ctx, `SELECT id, journal_id, request_hash FROM customer_credit_refunds WHERE org_id=$1 AND idempotency_key=$2`,
		access.ID, key).Scan(&rid, &rjid, &rhash)
	if err == nil {
		if string(rhash) != string(hash[:]) {
			writeError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"refund_id": rid, "journal_id": rjid})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}

	var status string
	var total int64
	var creditDate time.Time
	err = tx.QueryRow(ctx, `SELECT status, total_cents, credit_date FROM customer_credit_notes WHERE org_id=$1 AND id=$2 FOR UPDATE`,
		access.ID, creditID).Scan(&status, &total, &creditDate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status != "open" {
		writeError(w, http.StatusUnprocessableEntity, "credit_not_open")
		return
	}
	if date.Before(creditDate) {
		writeError(w, http.StatusUnprocessableEntity, "refund_before_credit")
		return
	}
	var applied, refunded int64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND credit_note_id=$2),0),
		COALESCE((SELECT sum(amount_cents) FROM customer_credit_refunds WHERE org_id=$1 AND credit_note_id=$2),0)`,
		access.ID, creditID).Scan(&applied, &refunded); err != nil {
		serverError(w, err)
		return
	}
	if in.AmountCents > total-applied-refunded {
		writeError(w, http.StatusUnprocessableEntity, "credit_exceeds_balance")
		return
	}
	acct, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	jid, err := postJournal(ctx, tx, access.ID, u.ID, date, "Credit refund paid", "credit-refund:"+key, hash,
		acct[acctAR], acct[acctBank], in.AmountCents)
	if err != nil {
		serverError(w, err)
		return
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO customer_credit_refunds (org_id, credit_note_id, amount_cents, refund_date, idempotency_key, request_hash, journal_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		access.ID, creditID, in.AmountCents, date, key, hash[:], jid, u.ID).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'credit_note.refunded',jsonb_build_object('refund_id',$3::bigint,'credit_note_id',$4::bigint))`,
		access.ID, u.ID, id, creditID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"refund_id": id, "journal_id": jid, "credit_balance_cents": total - applied - refunded - in.AmountCents})
}
