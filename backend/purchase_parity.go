package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 2b purchase-side rules: the bill-forwarding address, one payment covering several bills,
// and raising an approvable bill from an approved purchase order.

// billsForwardingAddress is the read-only inbox address shown on the bills list. Tidal does not
// receive mail yet, so nothing is stored: the address is derived from the company.
func billsForwardingAddress(orgID int64) string {
	return fmt.Sprintf("bills+org%d@inbox.tidalapp.sg", orgID)
}

type batchPaymentInput struct {
	OrgID          int64  `json:"org_id"`
	PaymentDate    string `json:"payment_date"`
	Reference      string `json:"reference"`
	AccountID      int64  `json:"account_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Allocations    []struct {
		BillID      int64 `json:"bill_id"`
		AmountCents int64 `json:"amount_cents"`
	} `json:"allocations"`
}

// payBillsBatch: POST /api/v1/bill-payments/batch. One date, reference and money account cover
// several approved unpaid bills; each bill gets its own payment row and the journal is posted once.
func (a *app) payBillsBatch(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in batchPaymentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reference = strings.TrimSpace(in.Reference)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	date, okDate := parseDay(in.PaymentDate)
	if !okDate || len(in.Allocations) == 0 || len(in.Allocations) > 200 || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 60 ||
		strings.ContainsRune(in.IdempotencyKey, 0) || !validTaxText(in.Reference, 100) {
		writeError(w, 400, "invalid_input")
		return
	}
	seen := map[int64]bool{}
	for _, al := range in.Allocations {
		if al.BillID <= 0 || al.AmountCents <= 0 || al.AmountCents > maxMoneyCents || seen[al.BillID] {
			writeError(w, 400, "invalid_input")
			return
		}
		seen[al.BillID] = true
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
	accounts, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.AccountID == 0 {
		in.AccountID = accounts[acctBank]
	}
	var accountType string
	if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.AccountID).Scan(&accountType); err != nil || accountType != "asset" {
		writeError(w, 400, "invalid_bank_account")
		return
	}
	key := "bill-batch:" + in.IdempotencyKey
	var existing int64
	if err := tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND idempotency_key=$2`, access.ID, key).Scan(&existing); err == nil {
		ids, err := billPaymentsForJournal(ctx, tx, access.ID, existing)
		if err != nil {
			serverError(w, err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"journal_id": existing, "payment_ids": ids})
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	// Every selected bill must be posted, unpaid to the amount chosen, and owed to this company.
	var total int64
	for i, al := range in.Allocations {
		var state string
		var billDate time.Time
		var billTotal, credited, paid int64
		err := tx.QueryRow(ctx, `SELECT status,bill_date,total_cents,
			COALESCE((SELECT sum(total_cents) FROM supplier_credits WHERE org_id=b.org_id AND bill_id=b.id),0),
			COALESCE((SELECT sum(amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id
				AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0)
			FROM bills b WHERE b.org_id=$1 AND b.id=$2 FOR UPDATE`, access.ID, al.BillID).Scan(&state, &billDate, &billTotal, &credited, &paid)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "posted" {
			writeError(w, 422, "bill_not_posted")
			return
		}
		if date.Before(billDate) {
			writeError(w, 422, "payment_before_bill")
			return
		}
		if al.AmountCents > billTotal-credited-paid {
			writeError(w, 422, "overpayment")
			return
		}
		total += al.AmountCents
		in.Allocations[i].AmountCents = al.AmountCents
	}
	canonical, _ := json.Marshal([]any{in.PaymentDate, in.Reference, in.AccountID, in.Allocations})
	hash := sha256.Sum256(canonical)
	jid, err := postJournal(ctx, tx, access.ID, u.ID, date, "Batch payment of supplier bills", key, hash, accounts[acctAP], in.AccountID, total)
	if err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	ids := make([]int64, 0, len(in.Allocations))
	for _, al := range in.Allocations {
		var pid int64
		rowKey := fmt.Sprintf("%s:%d", in.IdempotencyKey, al.BillID)
		if err := tx.QueryRow(ctx, `INSERT INTO bill_payments(org_id,bill_id,amount_cents,payment_date,reference,idempotency_key,request_hash,journal_id,created_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, access.ID, al.BillID, al.AmountCents, date, in.Reference, rowKey, hash[:], jid, u.ID).Scan(&pid); err != nil {
			serverError(w, err)
			return
		}
		ids = append(ids, pid)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bill.batch_payment',jsonb_build_object('journal_id',$3::bigint,'bills',$4::int))`, access.ID, u.ID, jid, len(ids)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"journal_id": jid, "payment_ids": ids, "total_cents": total})
}

func billPaymentsForJournal(ctx context.Context, tx pgx.Tx, orgID, journalID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM bill_payments WHERE org_id=$1 AND journal_id=$2 ORDER BY id`, orgID, journalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// billFromPurchaseOrder: POST /api/v1/purchase-orders/{id}/bill. The copy carries the supplier,
// date, reference, quantities and prices, and adds the account and tax from each PO line plus a due
// date from the payment terms, so it can be approved without edits.
func (a *app) billFromPurchaseOrder(w http.ResponseWriter, r *http.Request, u currentUser) {
	poID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID        int64  `json:"org_id"`
		BillDate     string `json:"bill_date"`
		PaymentTerms int    `json:"payment_terms_days"`
		DueDate      string `json:"due_date"`
	}
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
	var status, poNumber, poDate string
	var contactID, poTotal, billed int64
	var commercial []byte
	err = tx.QueryRow(ctx, `SELECT status,po_number,po_date::text,contact_id,total_cents,billed_cents,commercial
		FROM purchase_orders WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, poID).Scan(&status, &poNumber, &poDate, &contactID, &poTotal, &billed, &commercial)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status != "issued" && status != "received" {
		writeError(w, 409, "purchase_order_not_approved")
		return
	}
	if billed >= poTotal {
		writeError(w, 409, "purchase_order_already_billed")
		return
	}
	rows, err := tx.Query(ctx, `SELECT lines FROM purchase_orders WHERE org_id=$1 AND id=$2`, access.ID, poID)
	if err != nil {
		serverError(w, err)
		return
	}
	var raw []byte
	if rows.Next() {
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
	}
	rows.Close()
	var poLines []creditLine
	if err := json.Unmarshal(raw, &poLines); err != nil || len(poLines) == 0 {
		serverError(w, errors.New("purchase order lines unreadable"))
		return
	}
	// Terms: typed on this request, else the PO's own, else the supplier's bill terms.
	terms := in.PaymentTerms
	if terms == 0 {
		var comm struct {
			PaymentTermsDays int `json:"payment_terms_days"`
		}
		_ = json.Unmarshal(commercial, &comm)
		terms = comm.PaymentTermsDays
	}
	if terms == 0 {
		var defaultTerms int
		var billTerms *int
		if err := tx.QueryRow(ctx, `SELECT payment_terms_days,bill_payment_terms_days FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, contactID).Scan(&defaultTerms, &billTerms); err != nil {
			serverError(w, err)
			return
		}
		if billTerms != nil {
			defaultTerms = *billTerms
		}
		terms = defaultTerms
	}
	if terms < 0 || terms > 365 {
		writeError(w, 400, "invalid_input")
		return
	}
	billDate := poDate
	if in.BillDate != "" {
		if _, ok := parseDay(in.BillDate); !ok {
			writeError(w, 400, "invalid_input")
			return
		}
		billDate = in.BillDate
	}
	issuedDay, _ := parseDay(billDate)
	dueDate := day(issuedDay.AddDate(0, 0, terms))
	if in.DueDate != "" {
		d, okDue := parseDay(in.DueDate)
		if !okDue || d.Before(issuedDay) {
			writeError(w, 400, "invalid_input")
			return
		}
		dueDate = in.DueDate
	}
	bill := billInput{OrgID: access.ID, ContactID: contactID, SupplierReference: poNumber, BillDate: billDate,
		DueDate: dueDate, PaymentTermsDays: terms}
	for _, l := range poLines {
		bill.Lines = append(bill.Lines, billLineIn{ItemID: l.ItemID, AccountID: l.AccountID, Description: l.Description,
			Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents, GSTCode: l.GSTCode, Tracking: l.Tracking})
	}
	gstRegistered, err := gstRegisteredOn(ctx, tx, access.ID, day(issuedDay))
	if err != nil {
		serverError(w, err)
		return
	}
	rateBP, err := taxRateBPOn(ctx, tx, "SR", "purchase", day(issuedDay), func() int64 {
		rate, _ := gstRateBPOn(ctx, tx, day(issuedDay))
		return rate
	})
	if err != nil {
		serverError(w, err)
		return
	}
	catalogue, err := taxCatalogueOn(ctx, tx, "purchase", billDate)
	if err != nil {
		serverError(w, err)
		return
	}
	lines, net, gst, selfGST, code := validBillRates(&bill, gstRegistered, rateBP, catalogue)
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if st, code, err := checkBillRefs(ctx, tx, access.ID, &bill); err != nil {
		serverError(w, err)
		return
	} else if code != "" {
		writeError(w, st, code)
		return
	}
	total := net + gst
	var billID int64
	err = tx.QueryRow(ctx, `INSERT INTO bills(org_id,contact_id,supplier_reference,bill_date,due_date,notes,total_cents,gst_cents,self_gst_cents,created_by,order_number,payment_terms_days)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, access.ID, contactID, poNumber, billDate, dueDate,
		"Copied from purchase order "+poNumber, total, gst, selfGST, u.ID, poNumber, terms).Scan(&billID)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "bill_reference_exists")
			return
		}
		serverError(w, err)
		return
	}
	for i, l := range lines {
		if okTrack, terr := validTracking(ctx, tx, access.ID, l.Tracking); terr != nil {
			serverError(w, terr)
			return
		} else if !okTrack {
			writeError(w, 422, "invalid_tracking")
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents,discount_cents,discount_basis_points,item_id,gst_code,gst_rate_bp,gst_cents,tracking)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11::bigint,0),$12,$13,$14,$15)`, access.ID, billID, l.LineNo,
			bill.Lines[i].AccountID, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, bill.Lines[i].DiscountCents,
			bill.Lines[i].DiscountBasisPoints, l.ItemID, l.GSTCode, l.GSTRateBP, l.GSTCents, trackingJSON(l.Tracking)); err != nil {
			serverError(w, err)
			return
		}
	}
	billed += total
	poStatus := status
	if billed >= poTotal {
		poStatus = "billed"
	}
	if _, err := tx.Exec(ctx, `UPDATE purchase_orders SET billed_cents=$3,status=$4,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, poID, billed, poStatus); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'purchase_order.billed',jsonb_build_object('purchase_order_id',$3::bigint,'bill_id',$4::bigint))`, access.ID, u.ID, poID, billID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"bill_id": billID, "bill_number": "", "total_cents": total,
		"purchase_order_status": poStatus, "billed_cents": billed, "billed_percent": billed * 100 / poTotal})
}
