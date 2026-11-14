package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Transfer money: one balanced journal moving an amount between two ledger accounts. No bank feed is
// involved, so this is a plain ledger transfer (deliberate Tidal difference from Xero's bank transfer).
type transferInput struct {
	OrgID          int64  `json:"org_id"`
	FromAccountID  int64  `json:"from_account_id"`
	ToAccountID    int64  `json:"to_account_id"`
	EntryDate      string `json:"entry_date"`
	AmountCents    int64  `json:"amount_cents"`
	Reference      string `json:"reference"`
	IdempotencyKey string `json:"idempotency_key"`
}

func validTransfer(in *transferInput) bool {
	in.Reference = strings.TrimSpace(in.Reference)
	if in.FromAccountID <= 0 || in.ToAccountID <= 0 || in.FromAccountID == in.ToAccountID {
		return false
	}
	if in.AmountCents <= 0 || in.AmountCents > maxMoneyCents {
		return false
	}
	if !validTaxText(in.Reference, 200) {
		return false
	}
	if len(in.IdempotencyKey) < 1 || len(in.IdempotencyKey) > 93 || strings.ContainsRune(in.IdempotencyKey, 0) {
		return false
	}
	_, ok := parseDay(in.EntryDate)
	return ok
}

func (a *app) createTransfer(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in transferInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validTransfer(&in) {
		writeError(w, http.StatusBadRequest, "invalid_transfer")
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
	day, _ := parseDay(in.EntryDate)
	ctx := r.Context()
	key := "transfer:" + in.IdempotencyKey
	hash := sha256.Sum256([]byte(strconv.FormatInt(in.FromAccountID, 10) + ">" + strconv.FormatInt(in.ToAccountID, 10) + ":" + in.EntryDate + ":" + strconv.FormatInt(in.AmountCents, 10) + ":" + in.Reference))
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Serialise per company so a retry with the same key cannot double-post.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var existing int64
	var existingHash []byte
	switch err := tx.QueryRow(ctx, `SELECT id,request_hash FROM journals WHERE org_id=$1 AND idempotency_key=$2`, access.ID, key).Scan(&existing, &existingHash); {
	case err == nil:
		if string(existingHash) != string(hash[:]) {
			writeError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": existing})
		return
	case !errors.Is(err, pgx.ErrNoRows):
		serverError(w, err)
		return
	}
	for _, id := range []int64{in.FromAccountID, in.ToAccountID} {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type='asset')`, access.ID, id).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if !exists {
			writeError(w, http.StatusBadRequest, "invalid_account")
			return
		}
	}
	memo := "Transfer"
	if in.Reference != "" {
		memo = "Transfer " + in.Reference
	}
	id, err := postJournal(ctx, tx, access.ID, u.ID, day, memo, key, hash, in.ToAccountID, in.FromAccountID, in.AmountCents)
	if isPeriodClosed(err) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"journal_id": id, "from": in.FromAccountID, "to": in.ToAccountID, "amount_cents": in.AmountCents})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'transfer.created',$3)`, access.ID, u.ID, detail); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// listTransfers returns recent transfer journals so the page can show what already moved.
func (a *app) listTransfers(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT j.id,j.entry_date::text,j.memo,l.debit_cents,l.account_id,
		(SELECT c.account_id FROM journal_lines c WHERE c.org_id=j.org_id AND c.journal_id=j.id AND c.credit_cents>0 LIMIT 1)
		FROM journals j JOIN journal_lines l ON l.org_id=j.org_id AND l.journal_id=j.id
		WHERE j.org_id=$1 AND j.idempotency_key LIKE 'transfer:%' AND l.debit_cents>0
		ORDER BY j.entry_date DESC,j.id DESC LIMIT 100`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type transfer struct {
		ID            int64  `json:"id"`
		EntryDate     string `json:"entry_date"`
		Memo          string `json:"memo"`
		AmountCents   int64  `json:"amount_cents"`
		ToAccountID   int64  `json:"to_account_id"`
		FromAccountID int64  `json:"from_account_id"`
	}
	out := []transfer{}
	for rows.Next() {
		var t transfer
		if err := rows.Scan(&t.ID, &t.EntryDate, &t.Memo, &t.AmountCents, &t.ToAccountID, &t.FromAccountID); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": out})
}
