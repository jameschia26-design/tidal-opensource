package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/taxrules"
)

// Sales receipts post at once: Dr deposit account, Cr income per line. Discount/shipping/adjustment post against the first line's income account.
// ponytail: no edit or void after save (append-only); reverse with a credit note until a void flow exists. "Save and send" is not offered: Tidal sends no email.

type salesReceiptInput struct {
	OrgID            int64            `json:"org_id"`
	ContactID        int64            `json:"contact_id"`
	Number           string           `json:"receipt_number"`
	ReceiptDate      string           `json:"receipt_date"`
	Lines            []poLineIn       `json:"lines"`
	Commercial       commercialFields `json:"commercial"`
	PaymentMode      string           `json:"payment_mode"`
	DepositAccountID int64            `json:"deposit_account_id"`
	Reference        string           `json:"reference"`
	Notes            string           `json:"notes"`
	Terms            string           `json:"terms"`
	IdempotencyKey   string           `json:"idempotency_key"`
}

func (a *app) createSalesReceipt(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in salesReceiptInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Number = strings.TrimSpace(in.Number)
	date, dok := parseDay(in.ReceiptDate)
	if in.ContactID <= 0 || in.DepositAccountID <= 0 || !dok || !validTaxText(in.Number, 50) || !validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || !validTaxText(in.Terms, 4000) ||
		len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 100 {
		writeError(w, 400, "invalid_input")
		return
	}
	lines, subtotal, valid := buildPOLines(in.Lines)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	total, valid := commercialTotal(subtotal, in.Commercial)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	commercial, _ := json.Marshal(in.Commercial)
	data, _ := json.Marshal(lines)
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.validPaymentMode(r.Context(), access.ID, in.PaymentMode) {
		writeError(w, 400, "invalid_input")
		return
	}
	// GST codes are part of the request; omitted when no line asks for tax so older receipts replay unchanged.
	codes := ""
	for _, l := range in.Lines {
		if c := strings.ToUpper(strings.TrimSpace(l.GSTCode)); c != "" && c != "NONE" {
			codes = "|gst"
			for _, l := range in.Lines {
				codes += ":" + strings.ToUpper(strings.TrimSpace(l.GSTCode))
			}
			break
		}
	}
	reqHash := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%s|%s|%d|%s|%s|%s|%s|%d", in.ContactID, in.Number, in.ReceiptDate, data, commercial, in.DepositAccountID, in.PaymentMode, in.Reference, in.Notes, in.Terms, total) + codes))
	keySum := sha256.Sum256([]byte(in.IdempotencyKey))
	key := "sr:" + hex.EncodeToString(keySum[:])[:40]
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var prevID, prevJournal, prevTotal int64
	var prevHash []byte
	err = tx.QueryRow(ctx, `SELECT id,journal_id,request_hash,total_cents FROM sales_receipts WHERE org_id=$1 AND idempotency_key=$2`, access.ID, key).Scan(&prevID, &prevJournal, &prevHash, &prevTotal)
	if err == nil {
		if !bytes.Equal(prevHash, reqHash[:]) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]any{"id": prevID, "journal_id": prevJournal, "total_cents": prevTotal})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	registered, regErr := gstRegisteredOn(ctx, tx, access.ID, day(date))
	if err = regErr; err != nil {
		serverError(w, err)
		return
	}
	taxedLines := make([]creditLine, len(lines))
	var gstTotal int64
	for i, l := range lines {
		cl := creditLine{poLine: l, GSTCode: "NONE"}
		switch strings.ToUpper(strings.TrimSpace(in.Lines[i].GSTCode)) {
		case "", "NONE":
		case "SR":
			c := in.Commercial
			if !registered || c.DiscountCents != 0 || c.DiscountBasisPoints != 0 || c.ShippingCents != 0 || c.AdjustmentCents != 0 {
				writeError(w, 422, "unsupported_gst")
				return
			}
			cl.GSTCode = "SR"
			cl.GSTRateBP = taxrules.GSTRateBP(date)
			cl.GSTCents = (l.AmountCents*cl.GSTRateBP + 5000) / 10000
			gstTotal += cl.GSTCents
		default:
			writeError(w, 422, "unsupported_gst")
			return
		}
		taxedLines[i] = cl
	}
	total += gstTotal
	if total > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	data, _ = json.Marshal(taxedLines)
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, 422, "invalid_contact")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var typ string
	err = tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.DepositAccountID).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "asset") {
		writeError(w, 422, "invalid_deposit_account")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	for _, l := range lines {
		err = tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, l.AccountID).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "revenue") {
			writeError(w, 422, "invalid_revenue_account")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if in.Number == "" {
		var n int64
		if err = tx.QueryRow(ctx, `SELECT count(*)+1 FROM sales_receipts WHERE org_id=$1`, access.ID).Scan(&n); err != nil {
			serverError(w, err)
			return
		}
		in.Number = fmt.Sprintf("SR-%05d", n)
	}
	var jid int64
	if err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, access.ID, date, "Sales receipt "+in.Number, key, reqHash[:], u.ID).Scan(&jid); err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,1,$3,$4,0)`, access.ID, jid, in.DepositAccountID, total); err != nil {
		serverError(w, err)
		return
	}
	n := 1
	for _, l := range lines {
		if l.AmountCents == 0 {
			continue
		}
		n++
		if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,0,$5)`, access.ID, jid, n, l.AccountID, l.AmountCents); err != nil {
			serverError(w, err)
			return
		}
	}
	// discount makes total < subtotal: Dr income; shipping/adjustment makes total > subtotal: Cr income.
	if diff := total - subtotal - gstTotal; diff != 0 {
		n++
		debit, credit := -diff, int64(0)
		if diff > 0 {
			debit, credit = 0, diff
		}
		if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,$6)`, access.ID, jid, n, lines[0].AccountID, debit, credit); err != nil {
			serverError(w, err)
			return
		}
	}
	if gstTotal > 0 {
		ids, err := accountIDs(ctx, tx, access.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		n++
		if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,0,$5)`, access.ID, jid, n, ids["2300"], gstTotal); err != nil {
			serverError(w, err)
			return
		}
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO sales_receipts(org_id,contact_id,receipt_number,receipt_date,lines,commercial,total_cents,payment_mode,deposit_account_id,reference,notes,terms,journal_id,idempotency_key,request_hash,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`, access.ID, in.ContactID, in.Number, date, data, commercial, total, in.PaymentMode, in.DepositAccountID, in.Reference, in.Notes, in.Terms, jid, key, reqHash[:], u.ID).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "sales_receipts_org_id_receipt_number_key") {
			writeError(w, 409, "duplicate_number")
			return
		}
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'sales_receipt.created',jsonb_build_object('sales_receipt_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
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
	writeJSON(w, 201, map[string]any{"id": id, "receipt_number": in.Number, "journal_id": jid, "total_cents": total})
}

func (a *app) listSalesReceipts(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT s.id,s.contact_id,c.name,s.receipt_number,s.receipt_date::text,s.lines,s.commercial,s.total_cents,s.payment_mode,s.deposit_account_id,s.reference,s.notes,s.terms
		FROM sales_receipts s JOIN contacts c ON c.org_id=s.org_id AND c.id=s.contact_id WHERE s.org_id=$1 ORDER BY s.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type receipt struct {
		ID               int64           `json:"id"`
		ContactID        int64           `json:"contact_id"`
		CustomerName     string          `json:"customer_name"`
		Number           string          `json:"receipt_number"`
		ReceiptDate      string          `json:"receipt_date"`
		Lines            json.RawMessage `json:"lines"`
		Commercial       json.RawMessage `json:"commercial"`
		TotalCents       int64           `json:"total_cents"`
		PaymentMode      string          `json:"payment_mode"`
		DepositAccountID int64           `json:"deposit_account_id"`
		Reference        string          `json:"reference"`
		Notes            string          `json:"notes"`
		Terms            string          `json:"terms"`
	}
	out := []receipt{}
	for rows.Next() {
		var s receipt
		if err := rows.Scan(&s.ID, &s.ContactID, &s.CustomerName, &s.Number, &s.ReceiptDate, &s.Lines, &s.Commercial, &s.TotalCents, &s.PaymentMode, &s.DepositAccountID, &s.Reference, &s.Notes, &s.Terms); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"sales_receipts": out})
}
