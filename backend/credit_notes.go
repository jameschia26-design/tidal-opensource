package main

import (
	"net/http"
	"strings"
)

// Credit notes reduce a posted invoice: they reverse its revenue and output GST pro rata and clear the
// receivable. They never edit the invoice.

type creditNoteInput struct {
	OrgID       int64  `json:"org_id"`
	CreditDate  string `json:"credit_date"`
	Reason      string `json:"reason"`
	AmountCents int64  `json:"amount_cents"`
}

func (a *app) createCreditNote(w http.ResponseWriter, r *http.Request, u currentUser) {
	invID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in creditNoteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	date, ok := parseDay(in.CreditDate)
	if !ok || in.Reason == "" || !validTaxText(in.Reason, 500) || in.AmountCents < 1 || in.AmountCents > maxMoneyCents {
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
	// The importer calls the same function, so a credit note posted either way goes through one path.
	id, jid, code, err := postCreditNoteTx(ctx, tx, access.ID, u.ID, invID, in.AmountCents, date, in.Reason, nil, false)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "journal_id": jid, "amount_cents": in.AmountCents})
}

func (a *app) listCreditNotes(w http.ResponseWriter, r *http.Request, u currentUser) {
	invID, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id, credit_date::text, reason, amount_cents, journal_id FROM credit_notes WHERE org_id=$1 AND invoice_id=$2 ORDER BY id`, access.ID, invID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type note struct {
		ID          int64  `json:"id"`
		CreditDate  string `json:"credit_date"`
		Reason      string `json:"reason"`
		AmountCents int64  `json:"amount_cents"`
		JournalID   int64  `json:"journal_id"`
	}
	out := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.CreditDate, &n.Reason, &n.AmountCents, &n.JournalID); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credit_notes": out})
}
