package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// One payment received (or made) settles several posted invoices (or bills) at once. Each allocation is also
// written to payments / bill_payments so balances, aging and bank matching keep working.
// Received payments may exceed the allocated total: the surplus is recorded in payment_excess as a customer credit.
// ponytail: paid amounts must still equal the allocated total. Refunds of an excess, prepayments before an invoice date,
// "Save as Draft" for vendor payments and custom fields are not built. Refunds of credit notes exist separately.

type allocIn struct {
	DocID       int64 `json:"id"`
	AmountCents int64 `json:"amount_cents"`
}

type allocatedPaymentIn struct {
	OrgID             int64     `json:"org_id"`
	ContactID         int64     `json:"contact_id"`
	Number            string    `json:"payment_number"`
	Date              string    `json:"payment_date"`
	Mode              string    `json:"payment_mode"`
	AccountID         int64     `json:"account_id"` // deposit-to (received) or paid-through (made)
	AmountCents       int64     `json:"amount_cents"`
	BankChargeCents   int64     `json:"bank_charge_cents"`
	BankChargeAccount int64     `json:"bank_charge_account_id"`
	Reference         string    `json:"reference"`
	Notes             string    `json:"notes"`
	Allocations       []allocIn `json:"allocations"`
	IdempotencyKey    string    `json:"idempotency_key"`
	DraftID           int64     `json:"draft_id"`
}

type allocSpec struct {
	prefix, memo, table, kind string
	received                  bool
}

var receivedSpec = allocSpec{prefix: "PR", memo: "Payment received ", table: "payments_received", kind: "payments-received", received: true}
var madeSpec = allocSpec{prefix: "PM", memo: "Payment made ", table: "payments_made", kind: "payments-made"}

func (a *app) createPaymentReceived(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.createAllocatedPayment(w, r, u, receivedSpec)
}
func (a *app) createPaymentMade(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.createAllocatedPayment(w, r, u, madeSpec)
}

func (a *app) createAllocatedPayment(w http.ResponseWriter, r *http.Request, u currentUser, sp allocSpec) {
	var in allocatedPaymentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Number = strings.TrimSpace(in.Number)
	date, dok := parseDay(in.Date)
	if in.DraftID < 0 || in.ContactID <= 0 || in.AccountID <= 0 || !dok || in.AmountCents < 1 || in.AmountCents > maxMoneyCents || in.BankChargeCents < 0 || !validTaxText(in.Number, 50) ||
		!validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 100 ||
		len(in.Allocations) == 0 || len(in.Allocations) > 200 {
		writeError(w, 400, "invalid_input")
		return
	}
	if !sp.received && (in.BankChargeCents != 0 || in.BankChargeAccount != 0) {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.BankChargeCents > in.AmountCents || (in.BankChargeCents == 0) != (in.BankChargeAccount == 0) {
		writeError(w, 400, "invalid_input")
		return
	}
	sort.Slice(in.Allocations, func(i, j int) bool { return in.Allocations[i].DocID < in.Allocations[j].DocID })
	var allocated int64
	for i, al := range in.Allocations {
		if al.DocID <= 0 || al.AmountCents < 1 || al.AmountCents > maxMoneyCents || (i > 0 && al.DocID == in.Allocations[i-1].DocID) {
			writeError(w, 400, "invalid_input")
			return
		}
		allocated += al.AmountCents
	}
	// Received payments may exceed what they settle; the surplus becomes a customer credit. Bills must match exactly.
	if allocated > in.AmountCents || (!sp.received && allocated != in.AmountCents) {
		writeError(w, 422, "allocation_mismatch")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.validPaymentMode(r.Context(), access.ID, in.Mode) {
		writeError(w, 400, "invalid_input")
		return
	}
	allocJSON, _ := json.Marshal(in.Allocations)
	hashInput := fmt.Sprintf("%s|%d|%s|%s|%s|%d|%d|%d|%d|%s|%s|%s", sp.table, in.ContactID, in.Number, in.Date, in.Mode, in.AccountID, in.AmountCents, in.BankChargeCents, in.BankChargeAccount, in.Reference, in.Notes, allocJSON)
	if in.DraftID != 0 {
		hashInput += fmt.Sprintf("|draft:%d", in.DraftID)
	}
	reqHash := sha256.Sum256([]byte(hashInput))
	keySum := sha256.Sum256([]byte(sp.table + "|" + in.IdempotencyKey))
	key := strings.ToLower(sp.prefix) + ":" + hex.EncodeToString(keySum[:])[:40]
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, fmt.Sprintf("%d:%s", access.ID, sp.table)); err != nil {
		serverError(w, err)
		return
	}
	// Acquire company lock before document locks, matching other ledger writes.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var prevID, prevJournal int64
	var prevHash []byte
	err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT id,journal_id,request_hash FROM %s WHERE org_id=$1 AND idempotency_key=$2`, sp.table), access.ID, key).Scan(&prevID, &prevJournal, &prevHash)
	if err == nil {
		if !bytes.Equal(prevHash, reqHash[:]) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]any{"id": prevID, "journal_id": prevJournal, "amount_cents": in.AmountCents})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	// Lock and consume the draft in the same transaction as the payment.
	if in.DraftID != 0 {
		side := "made"
		if sp.received {
			side = "received"
		}
		tag, err := tx.Exec(ctx, `DELETE FROM payment_drafts WHERE org_id=$1 AND id=$2 AND side=$3`, access.ID, in.DraftID, side)
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
	}
	// Contact must be the right side. Account must be an asset (and the charge account an expense).
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && ((sp.received && kind == "supplier") || (!sp.received && kind == "customer"))) {
		writeError(w, 422, "invalid_contact")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var typ string
	err = tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.AccountID).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "asset") {
		writeError(w, 422, "invalid_deposit_account")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if in.BankChargeAccount != 0 {
		err = tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.BankChargeAccount).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "expense") {
			writeError(w, 422, "invalid_charge_account")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	// Lock documents in id order (allocations are sorted) so two payments cannot both spend one balance.
	for _, al := range in.Allocations {
		var status string
		var total, paid int64
		var docDate time.Time
		var contact int64
		var q string
		if sp.received {
			q = `SELECT status,total_cents,invoice_date,contact_id,COALESCE((SELECT SUM(amount_cents) FROM payments p WHERE p.org_id=d.org_id AND p.invoice_id=d.id),0) + COALESCE((SELECT SUM(amount_cents) FROM credit_notes n WHERE n.org_id=d.org_id AND n.invoice_id=d.id),0) + COALESCE((SELECT SUM(amount_cents) FROM customer_credit_applications c WHERE c.org_id=d.org_id AND c.invoice_id=d.id),0) FROM invoices d WHERE org_id=$1 AND id=$2 FOR UPDATE`
		} else {
			q = `SELECT status,total_cents,bill_date,contact_id,COALESCE((SELECT SUM(amount_cents) FROM bill_payments p WHERE p.org_id=d.org_id AND p.bill_id=d.id),0) FROM bills d WHERE org_id=$1 AND id=$2 FOR UPDATE`
		}
		err = tx.QueryRow(ctx, q, access.ID, al.DocID).Scan(&status, &total, &docDate, &contact, &paid)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && contact != in.ContactID) {
			writeError(w, 422, "invalid_allocation")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if status != "posted" {
			writeError(w, 422, "document_not_posted")
			return
		}
		if date.Before(docDate) {
			writeError(w, 422, "payment_before_document")
			return
		}
		if al.AmountCents > total-paid {
			writeError(w, 422, "overpayment")
			return
		}
	}
	if in.Number == "" {
		var n int64
		if err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*)+1 FROM %s WHERE org_id=$1`, sp.table), access.ID).Scan(&n); err != nil {
			serverError(w, err)
			return
		}
		in.Number = fmt.Sprintf("%s-%05d", sp.prefix, n)
	}
	acct, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var jid int64
	if err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, access.ID, date, sp.memo+in.Number, key, reqHash[:], u.ID).Scan(&jid); err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
		} else {
			serverError(w, err)
		}
		return
	}
	line := func(n int, account, debit, credit int64) error {
		_, e := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,$6)`, access.ID, jid, n, account, debit, credit)
		return e
	}
	if sp.received {
		// Dr deposit (net of bank charge), Dr bank charges, Cr receivables.
		err = line(1, in.AccountID, in.AmountCents-in.BankChargeCents, 0)
		if err == nil && in.BankChargeCents > 0 {
			err = line(2, in.BankChargeAccount, in.BankChargeCents, 0)
		}
		if err == nil {
			err = line(3, acct[acctAR], 0, in.AmountCents)
		}
	} else {
		err = line(1, acct[acctAP], in.AmountCents, 0)
		if err == nil {
			err = line(2, in.AccountID, 0, in.AmountCents)
		}
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var id int64
	chargeAcct := any(nil)
	if in.BankChargeAccount != 0 {
		chargeAcct = in.BankChargeAccount
	}
	if sp.received {
		err = tx.QueryRow(ctx, `INSERT INTO payments_received(org_id,contact_id,payment_number,payment_date,payment_mode,deposit_account_id,amount_cents,bank_charge_cents,bank_charge_account_id,reference,notes,allocations,journal_id,idempotency_key,request_hash,created_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`, access.ID, in.ContactID, in.Number, date, in.Mode, in.AccountID, in.AmountCents, in.BankChargeCents, chargeAcct, in.Reference, in.Notes, allocJSON, jid, key, reqHash[:], u.ID).Scan(&id)
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO payments_made(org_id,contact_id,payment_number,payment_date,payment_mode,paid_through_account_id,amount_cents,reference,notes,allocations,journal_id,idempotency_key,request_hash,created_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`, access.ID, in.ContactID, in.Number, date, in.Mode, in.AccountID, in.AmountCents, in.Reference, in.Notes, allocJSON, jid, key, reqHash[:], u.ID).Scan(&id)
	}
	if err != nil {
		if strings.Contains(err.Error(), "_payment_number_key") {
			writeError(w, 409, "duplicate_number")
			return
		}
		serverError(w, err)
		return
	}
	for _, al := range in.Allocations {
		subKey := fmt.Sprintf("%s:%d", key, al.DocID)
		if sp.received {
			_, err = tx.Exec(ctx, `INSERT INTO payments(org_id,invoice_id,amount_cents,payment_date,idempotency_key,request_hash,journal_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, access.ID, al.DocID, al.AmountCents, date, subKey, reqHash[:], jid, u.ID)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO bill_payments(org_id,bill_id,amount_cents,payment_date,reference,idempotency_key,request_hash,journal_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, access.ID, al.DocID, al.AmountCents, date, in.Reference, subKey, reqHash[:], jid, u.ID)
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if surplus := in.AmountCents - allocated; sp.received && surplus > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO payment_excess(org_id,payment_id,contact_id,amount_cents,excess_date) VALUES($1,$2,$3,$4,$5)`, access.ID, id, in.ContactID, surplus, date); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('id',$4::bigint))`, access.ID, u.ID, strings.ReplaceAll(sp.kind, "-", "_")+".created", id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "payment_number": in.Number, "journal_id": jid, "amount_cents": in.AmountCents})
}

func (a *app) listPaymentsReceived(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.listAllocatedPayments(w, r, u, receivedSpec)
}
func (a *app) listPaymentsMade(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.listAllocatedPayments(w, r, u, madeSpec)
}

func (a *app) listAllocatedPayments(w http.ResponseWriter, r *http.Request, u currentUser, sp allocSpec) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	acctCol, extra := "paid_through_account_id", "0::bigint,0::bigint"
	if sp.received {
		acctCol, extra = "deposit_account_id", "p.bank_charge_cents,COALESCE(p.bank_charge_account_id,0)"
	}
	rows, err := a.db.Query(r.Context(), fmt.Sprintf(`SELECT p.id,p.contact_id,c.name,p.payment_number,p.payment_date::text,p.payment_mode,p.%s,p.amount_cents,%s,p.reference,p.notes,p.allocations
		FROM %s p JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id WHERE p.org_id=$1 ORDER BY p.id DESC LIMIT 200`, acctCol, extra, sp.table), access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID                int64           `json:"id"`
		ContactID         int64           `json:"contact_id"`
		ContactName       string          `json:"contact_name"`
		Number            string          `json:"payment_number"`
		Date              string          `json:"payment_date"`
		Mode              string          `json:"payment_mode"`
		AccountID         int64           `json:"account_id"`
		AmountCents       int64           `json:"amount_cents"`
		BankChargeCents   int64           `json:"bank_charge_cents"`
		BankChargeAccount int64           `json:"bank_charge_account_id"`
		Reference         string          `json:"reference"`
		Notes             string          `json:"notes"`
		Allocations       json.RawMessage `json:"allocations"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.ContactID, &x.ContactName, &x.Number, &x.Date, &x.Mode, &x.AccountID, &x.AmountCents, &x.BankChargeCents, &x.BankChargeAccount, &x.Reference, &x.Notes, &x.Allocations); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"payments": out})
}
