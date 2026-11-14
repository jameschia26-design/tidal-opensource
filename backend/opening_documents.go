package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Opening documents (R17): a migration brings outstanding customer and supplier invoices as real, dated
// documents so aging and the control accounts agree. They post to Opening Balance Equity (3900), never revenue.

type openingDocInput struct {
	OrgID       int64  `json:"org_id"`
	Kind        string `json:"kind"` // customer or supplier
	ContactID   int64  `json:"contact_id"`
	Reference   string `json:"reference"`
	DocDate     string `json:"doc_date"`
	DueDate     string `json:"due_date"`
	AmountCents int64  `json:"amount_cents"`
}

// createOpeningDocument: POST /api/v1/opening-documents
func (a *app) createOpeningDocument(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in openingDocInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reference = strings.TrimSpace(in.Reference)
	date, dok := parseDay(in.DocDate)
	due, uok := parseDay(in.DueDate)
	if !validStage(in.Kind, "customer", "supplier") || in.ContactID <= 0 || !dok || !uok || due.Before(date) ||
		in.AmountCents <= 0 || in.AmountCents > maxMoneyCents || !validTaxText(in.Reference, 100) {
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
	wantRole, contactKind := "supplier", "supplier"
	if in.Kind == "customer" {
		wantRole, contactKind = "customer", "customer"
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_contact")
			return
		}
		serverError(w, err)
		return
	}
	if role != wantRole && role != "both" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_contact")
		return
	}
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var jid int64
	memo := "Opening " + contactKind + " balance " + in.Reference
	if err := tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by)
		VALUES($1,$2,$3,$4,sha256($5::bytea),$6) RETURNING id`,
		access.ID, date, memo, "opening:"+in.Kind+":"+itoaID(in.ContactID)+":"+in.Reference, []byte(memo), u.ID).Scan(&jid); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "opening_document_exists")
			return
		}
		serverError(w, err)
		return
	}
	// Customer: Dr AR / Cr OBE. Supplier: Dr OBE / Cr AP.
	legs := [][3]int64{{ids[acctAR], in.AmountCents, 0}, {ids[acctOBE], 0, in.AmountCents}}
	if in.Kind == "supplier" {
		legs = [][3]int64{{ids[acctOBE], in.AmountCents, 0}, {ids[acctAP], 0, in.AmountCents}}
	}
	for i, leg := range legs {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, access.ID, jid, i+1, leg[0], leg[1], leg[2], "Opening balance"); err != nil {
			serverError(w, err)
			return
		}
	}
	var docID int64
	if in.Kind == "customer" {
		var number int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(number),0)+1 FROM invoices WHERE org_id=$1`, access.ID).Scan(&number); err != nil {
			serverError(w, err)
			return
		}
		// Lines are written while the document is a draft (posted lines are immutable), then it is posted.
		if err := tx.QueryRow(ctx, `INSERT INTO invoices(org_id,contact_id,status,invoice_date,due_date,total_cents,created_by)
			VALUES($1,$2,'draft',$3,$4,$5,$6) RETURNING id`,
			access.ID, in.ContactID, date, due, in.AmountCents, u.ID).Scan(&docID); err != nil {
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents)
			VALUES($1,$2,1,$3,1,$4,$4)`, access.ID, docID, "Opening balance "+in.Reference, in.AmountCents); err != nil {
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE invoices SET status='posted',number=$3,journal_id=$4,posted_at=now() WHERE org_id=$1 AND id=$2`,
			access.ID, docID, number, jid); err != nil {
			serverError(w, err)
			return
		}
	} else {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&name); err != nil {
			serverError(w, err)
			return
		}
		if err := tx.QueryRow(ctx, `INSERT INTO bills(org_id,contact_id,supplier_reference,bill_date,due_date,status,total_cents,supplier_snapshot,created_by)
			VALUES($1,$2,$3,$4,$5,'draft',$6,jsonb_build_object('name',$7::text),$8) RETURNING id`,
			access.ID, in.ContactID, in.Reference, date, due, in.AmountCents, name, u.ID).Scan(&docID); err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "opening_document_exists")
				return
			}
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents)
			VALUES($1,$2,1,$3,$4,1,$5,$5)`, access.ID, docID, ids[acctOBE], "Opening balance "+in.Reference, in.AmountCents); err != nil {
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE bills SET status='posted',journal_id=$3,posted_at=now() WHERE org_id=$1 AND id=$2`, access.ID, docID, jid); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'opening_document.created',jsonb_build_object('kind',$3::text,'id',$4::bigint))`,
		access.ID, u.ID, in.Kind, docID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": docID, "kind": in.Kind, "journal_id": jid, "total_cents": in.AmountCents})
}

// itoaID is a tiny local helper so the journal key stays readable.
func itoaID(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
