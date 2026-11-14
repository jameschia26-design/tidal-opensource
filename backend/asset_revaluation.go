package main

import (
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"
)

// listAssetRevaluations: GET /api/v1/assets/{id}/revaluations?org_id
func (a *app) listAssetRevaluations(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT v.id,v.revalued_on::text,v.amount_cents,v.note,
		EXISTS(SELECT 1 FROM document_reversals r WHERE r.org_id=v.org_id AND r.doc_kind='asset_revaluation' AND r.doc_id=v.id) AS reversed
		FROM asset_revaluations v WHERE v.org_id=$1 AND v.asset_id=$2 ORDER BY v.revalued_on DESC,v.id DESC`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var rid, amount int64
		var date, note string
		var reversed bool
		if err := rows.Scan(&rid, &date, &amount, &note, &reversed); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": rid, "revalued_on": date, "amount_cents": amount, "note": note, "reversed": reversed})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revaluations": out})
}

// revalueAsset: POST /api/v1/assets/{id}/revalue {org_id,revalued_on,amount_cents,note}. A positive
// amount writes the asset up against the revaluation reserve, a negative one writes it down.
func (a *app) revalueAsset(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID       int64  `json:"org_id"`
		RevaluedOn  string `json:"revalued_on"`
		AmountCents int64  `json:"amount_cents"`
		Note        string `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	date, valid := parseDay(in.RevaluedOn)
	if !valid || in.AmountCents == 0 || len(in.Note) > 500 {
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
	var name string
	var cost int64
	if err := tx.QueryRow(ctx, `SELECT name,cost_cents FROM fixed_assets WHERE org_id=$1 AND id=$2 AND disposed_on IS NULL FOR UPDATE`, access.ID, id).Scan(&name, &cost); err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if cost+in.AmountCents < 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_amount")
		return
	}
	acct, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	amount := in.AmountCents
	debit, credit := acct[acctAssetCost], acct[acctRevaluation]
	if amount < 0 {
		amount = -amount
		debit, credit = credit, debit
	}
	key := "asset-revaluation:" + strconv.FormatInt(id, 10) + ":" + day(date) + ":" + strconv.FormatInt(in.AmountCents, 10)
	hash := sha256.Sum256([]byte(key))
	jid, err := postJournal(ctx, tx, access.ID, u.ID, date, "Revaluation: "+name, key, hash, debit, credit, amount)
	if err != nil {
		serverError(w, err)
		return
	}
	var rid int64
	if err := tx.QueryRow(ctx, `INSERT INTO asset_revaluations(org_id,asset_id,revalued_on,amount_cents,journal_id,note,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, access.ID, id, day(date), in.AmountCents, jid, strings.TrimSpace(in.Note), u.ID).Scan(&rid); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE fixed_assets SET cost_cents = cost_cents + $2 WHERE org_id=$1 AND id=$3`, access.ID, in.AmountCents, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": rid, "journal_id": jid})
}

// rollbackAssetRevaluation: POST /api/v1/assets/{id}/revaluations/{rid}/rollback {org_id,reversed_on}
// reverses the journal through the reversal log and restores the asset cost, so both entries remain
// visible. A revaluation can only be rolled back once.
func (a *app) rollbackAssetRevaluation(w http.ResponseWriter, r *http.Request, u currentUser) {
	assetID, ok := pathID(w, r)
	if !ok {
		return
	}
	rid, ok := parseOrgID(w, r.PathValue("rid"))
	if !ok || rid == 0 {
		return
	}
	var in struct {
		OrgID      int64  `json:"org_id"`
		ReversedOn string `json:"reversed_on"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	date, valid := parseDay(in.ReversedOn)
	if !valid {
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
	var amount, journal int64
	if err := tx.QueryRow(ctx, `SELECT amount_cents,journal_id FROM asset_revaluations WHERE org_id=$1 AND asset_id=$2 AND id=$3 FOR UPDATE`, access.ID, assetID, rid).Scan(&amount, &journal); err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var already bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM document_reversals WHERE org_id=$1 AND doc_kind='asset_revaluation' AND doc_id=$2)`, access.ID, rid).Scan(&already); err != nil {
		serverError(w, err)
		return
	}
	if already {
		writeError(w, http.StatusConflict, "already_reversed")
		return
	}
	acct, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	abs := amount
	debit, credit := acct[acctAssetCost], acct[acctRevaluation]
	if abs < 0 {
		abs = -abs
		debit, credit = credit, debit
	}
	key := "asset-revaluation-rollback:" + strconv.FormatInt(rid, 10)
	hash := sha256.Sum256([]byte(key))
	reversal, err := postJournal(ctx, tx, access.ID, u.ID, date, "Revaluation rollback", key, hash, credit, debit, abs)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO document_reversals(org_id,doc_kind,doc_id,original_journal_id,reversal_journal_id,reversal_date,created_by)
		VALUES($1,'asset_revaluation',$2,$3,$4,$5,$6)`, access.ID, rid, journal, reversal, day(date), u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE fixed_assets SET cost_cents = cost_cents - $2 WHERE org_id=$1 AND id=$3`, access.ID, amount, assetID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "reversal_journal_id": reversal})
}
