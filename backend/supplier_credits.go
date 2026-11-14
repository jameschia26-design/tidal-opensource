package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Supplier credits linked to a bill (D7). A credit reverses the bill's expense accounts and input GST in
// proportion to the credit and lowers the amount owed; a refund received posts to the bank.

type supplierCreditInput struct {
	OrgID      int64  `json:"org_id"`
	CreditDate string `json:"credit_date"`
	Reason     string `json:"reason"`
	NetCents   int64  `json:"net_cents"`
	GSTCents   int64  `json:"gst_cents"`
}

// billCreditLegs lists the bill's net amount per account and its input GST, so a credit can reverse them.
func billCreditLegs(ctx context.Context, tx pgx.Tx, orgID, billID int64) ([]accountAmount, int64, int64, error) {
	// Grouped by account AND tracking option, so a supplier credit reverses the same split the bill posted.
	rows, err := tx.Query(ctx, `SELECT account_id, SUM(amount_cents)::bigint, SUM(gst_cents)::bigint, tracking::text
		FROM bill_lines WHERE org_id=$1 AND bill_id=$2 GROUP BY account_id, tracking ORDER BY account_id`, orgID, billID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	legs := []accountAmount{}
	var net, gst int64
	for rows.Next() {
		var acct, amount, tax int64
		var tracking string
		if err := rows.Scan(&acct, &amount, &tax, &tracking); err != nil {
			return nil, 0, 0, err
		}
		if amount > 0 {
			legs = append(legs, accountAmount{acct, amount, tracking})
		}
		net += amount
		gst += tax
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	return legs, net, gst, nil
}

// createSupplierCredit: POST /api/v1/bills/{id}/credit-notes
func (a *app) createSupplierCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	billID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in supplierCreditInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	date, okDay := parseDay(in.CreditDate)
	if !okDay || in.Reason == "" || !validTaxText(in.Reason, 500) || in.NetCents < 0 || in.GSTCents < 0 ||
		in.NetCents+in.GSTCents <= 0 || in.NetCents+in.GSTCents > maxMoneyCents {
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
	// The importer calls the same function, so a supplier credit posted either way goes through one path.
	id, jid, code, err := postSupplierCreditTx(ctx, tx, access.ID, u.ID, billID, in.NetCents, in.GSTCents, date, in.Reason, nil, false, "")
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	var balance int64
	if err := tx.QueryRow(ctx, `SELECT total_cents
		- COALESCE((SELECT sum(total_cents) FROM supplier_credits WHERE org_id=$1 AND bill_id=$2),0)
		- COALESCE((SELECT sum(amount_cents) FROM bill_payments WHERE org_id=$1 AND bill_id=$2),0)
		FROM bills WHERE org_id=$1 AND id=$2`, access.ID, billID).Scan(&balance); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "journal_id": jid, "net_cents": in.NetCents, "gst_cents": in.GSTCents, "total_cents": in.NetCents + in.GSTCents, "balance_cents": balance})
}

// refundSupplierCredit: POST /api/v1/supplier-credits/{id}/refund — money received for the credit.
func (a *app) refundSupplierCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID         int64  `json:"org_id"`
		RefundDate    string `json:"refund_date"`
		BankAccountID int64  `json:"bank_account_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	on, okDay := parseDay(in.RefundDate)
	if !okDay || in.BankAccountID <= 0 {
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
	var total int64
	var refunded *time.Time
	if err := tx.QueryRow(ctx, `SELECT total_cents, refunded_on FROM supplier_credits WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&total, &refunded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if refunded != nil {
		writeError(w, http.StatusConflict, "credit_already_refunded")
		return
	}
	var bankType string
	if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.BankAccountID).Scan(&bankType); err != nil || bankType != "asset" {
		writeError(w, http.StatusBadRequest, "invalid_bank_account")
		return
	}
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	key := "supplier-credit-refund:" + strconv.FormatInt(id, 10)
	hash := sha256.Sum256([]byte(key))
	jid, err := postJournal(ctx, tx, access.ID, u.ID, on, "Supplier credit refund "+strconv.FormatInt(id, 10), key, hash, in.BankAccountID, ids[acctAP], total)
	if err != nil {
		if isPeriodClosed(err) {
			writeError(w, http.StatusUnprocessableEntity, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE supplier_credits SET refunded_on=$3::date, refund_journal_id=$4 WHERE org_id=$1 AND id=$2`, access.ID, id, day(on), jid); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'supplier_credit.refunded',jsonb_build_object('supplier_credit_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "journal_id": jid, "refunded_on": day(on)})
}

// ---- standalone supplier credits (own number, draft, list) ----

type supplierCreditLine struct {
	AccountID   int64  `json:"account_id"`
	Description string `json:"description"`
	NetCents    int64  `json:"net_cents"`
}

type standaloneCreditInput struct {
	OrgID       int64                `json:"org_id"`
	ContactID   int64                `json:"contact_id"`
	BillID      int64                `json:"bill_id"`
	AccountID   int64                `json:"account_id"`
	CreditDate  string               `json:"credit_date"`
	Reference   string               `json:"reference"`
	NetCents    int64                `json:"net_cents"`
	GSTCents    int64                `json:"gst_cents"`
	Currency    string               `json:"currency"`
	FXRateMicro int64                `json:"fx_rate_micro"`
	Lines       []supplierCreditLine `json:"lines"`
}

// createStandaloneSupplierCredit: POST /api/v1/supplier-credits. A credit can stand alone (its own
// expense account) or point at a posted bill; either way it starts as a draft with its own number.
func (a *app) createStandaloneSupplierCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in standaloneCreditInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Reference = strings.TrimSpace(in.Reference)
	date, okDay := parseDay(in.CreditDate)
	if !okDay || in.ContactID <= 0 || !validTaxText(in.Reference, 100) || in.NetCents < 0 || in.GSTCents < 0 ||
		in.NetCents+in.GSTCents <= 0 || in.NetCents+in.GSTCents > maxMoneyCents {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	currency, fxRate, okCurrency := resolveDocumentCurrency(ctx, a.db, access.ID, in.Currency, in.FXRateMicro, in.CreditDate)
	if !okCurrency {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
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
	var kind string
	if err := tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if kind == "customer" {
		writeError(w, 422, "invalid_supplier")
		return
	}
	if in.BillID > 0 {
		if in.Lines != nil {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		var supplier int64
		if err := tx.QueryRow(ctx, `SELECT contact_id FROM bills WHERE org_id=$1 AND id=$2`, access.ID, in.BillID).Scan(&supplier); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 404, "not_found")
				return
			}
			serverError(w, err)
			return
		}
		if supplier != in.ContactID {
			writeError(w, 422, "invalid_supplier")
			return
		}
		var billCurrency string
		if err := tx.QueryRow(ctx, `SELECT currency FROM bills WHERE org_id=$1 AND id=$2`, access.ID, in.BillID).Scan(&billCurrency); err != nil {
			serverError(w, err)
			return
		}
		if billCurrency != currency {
			writeError(w, 422, "currency_mismatch")
			return
		}
		in.AccountID = 0
	} else if in.Lines != nil {
		if len(in.Lines) == 0 || len(in.Lines) > 200 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		var sum int64
		for i := range in.Lines {
			in.Lines[i].Description = strings.TrimSpace(in.Lines[i].Description)
			if in.Lines[i].AccountID <= 0 || in.Lines[i].NetCents <= 0 || in.Lines[i].NetCents > maxMoneyCents || !validTaxText(in.Lines[i].Description, 200) {
				writeError(w, http.StatusBadRequest, "invalid_input")
				return
			}
			sum += in.Lines[i].NetCents
			if sum > maxMoneyCents {
				writeError(w, http.StatusBadRequest, "invalid_input")
				return
			}
			var accountType string
			if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.Lines[i].AccountID).Scan(&accountType); err != nil || accountType != "expense" {
				writeError(w, 422, "invalid_expense_account")
				return
			}
		}
		// The grid must foot to the header net; GST stays header-level, never per line.
		if sum != in.NetCents {
			writeError(w, http.StatusUnprocessableEntity, "invalid_input")
			return
		}
		in.AccountID = 0
	} else {
		var accountType string
		if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.AccountID).Scan(&accountType); err != nil || accountType != "expense" {
			writeError(w, 422, "invalid_expense_account")
			return
		}
	}
	// Lines ride along as nullable JSON: absent (NULL) keeps the old single-account path, so the
	// importer and PTB flows — which never send lines — are untouched. GST stays header-level, never
	// per line: the grid splits the net across expense accounts and the approve path credits one GST leg.
	var linesJSON *string
	if in.Lines != nil {
		raw, _ := json.Marshal(in.Lines)
		s := string(raw)
		linesJSON = &s
	}
	var id int64
	var number string
	if err := tx.QueryRow(ctx, `INSERT INTO supplier_credits(org_id,bill_id,contact_id,account_id,credit_date,reason,net_cents,gst_cents,total_cents,status,currency,fx_rate_micro,created_by,lines)
		VALUES($1,NULLIF($2::bigint,0),$3,NULLIF($4::bigint,0),$5::date,$6,$7,$8,$9,'draft',$10,$11,$12,$13::jsonb) RETURNING id,credit_number`,
		access.ID, in.BillID, in.ContactID, in.AccountID, day(date), in.Reference, in.NetCents, in.GSTCents,
		in.NetCents+in.GSTCents, currency, fxRate, u.ID, linesJSON).Scan(&id, &number); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'supplier_credit.created',jsonb_build_object('supplier_credit_id',$3::bigint,'draft',true))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "credit_number": number, "status": "draft", "total_cents": in.NetCents + in.GSTCents})
}

// approveStandaloneSupplierCredit: POST /api/v1/supplier-credits/{id}/approve. A draft becomes open
// and posts once: a bill-linked credit reverses the bill's expense and GST; a standalone credit
// debits what is owed to the supplier and credits its expense account.
func (a *app) approveStandaloneSupplierCredit(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID int64 `json:"org_id"`
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
	var status, reason, currency string
	var billID, contactID, accountID, net, gst, total, fxRate int64
	var creditDate time.Time
	var linesRaw []byte
	err = tx.QueryRow(ctx, `SELECT status,reason,COALESCE(bill_id,0),contact_id,COALESCE(account_id,0),net_cents,gst_cents,total_cents,credit_date,currency,fx_rate_micro,lines
		FROM supplier_credits WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&status, &reason, &billID, &contactID, &accountID, &net, &gst, &total, &creditDate, &currency, &fxRate, &linesRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status != "draft" {
		writeError(w, 409, "credit_not_draft")
		return
	}
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var debits, credits []accountAmount
	if billID > 0 {
		var billStatus, billDateText string
		var billTotal, billGST int64
		if err := tx.QueryRow(ctx, `SELECT status,bill_date::text,total_cents,gst_cents FROM bills WHERE org_id=$1 AND id=$2`, access.ID, billID).Scan(&billStatus, &billDateText, &billTotal, &billGST); err != nil {
			serverError(w, err)
			return
		}
		if billStatus != "posted" {
			writeError(w, 422, "bill_not_posted")
			return
		}
		if day(creditDate) < billDateText {
			writeError(w, 422, "credit_before_bill")
			return
		}
		var credited, creditedGST, paid int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(total_cents),0),COALESCE(SUM(gst_cents),0) FROM supplier_credits WHERE org_id=$1 AND bill_id=$2 AND status<>'draft'`, access.ID, billID).Scan(&credited, &creditedGST); err != nil {
			serverError(w, err)
			return
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM bill_payments p WHERE p.org_id=$1 AND p.bill_id=$2
			AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)`, access.ID, billID).Scan(&paid); err != nil {
			serverError(w, err)
			return
		}
		if credited+paid+total > billTotal || (gst > 0 && gst > billGST-creditedGST) {
			writeError(w, 422, "credit_exceeds_bill")
			return
		}
		legs, legNet, legGST, err := billCreditLegs(ctx, tx, access.ID, billID)
		if err != nil {
			serverError(w, err)
			return
		}
		if legGST > 0 {
			legs = append(legs, accountAmount{ids[acctGST], legGST, ""})
		}
		debits = []accountAmount{{ids[acctAP], total, ""}}
		credits = scaleLegs(legs, total, legNet+legGST)
	} else if linesRaw != nil {
		// Multi-line standalone credit: one credit leg per line account for the net, plus the
		// single header GST leg. GST is not split per line (deliberate: header-level tax only).
		var lines []supplierCreditLine
		if err := json.Unmarshal(linesRaw, &lines); err != nil || len(lines) == 0 || len(lines) > 200 {
			serverError(w, err)
			return
		}
		var sum int64
		for i := range lines {
			if lines[i].AccountID <= 0 || lines[i].NetCents <= 0 {
				writeError(w, 422, "invalid_expense_account")
				return
			}
			var accountType string
			if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, lines[i].AccountID).Scan(&accountType); err != nil || accountType != "expense" {
				writeError(w, 422, "invalid_expense_account")
				return
			}
			sum += lines[i].NetCents
		}
		if sum != net {
			writeError(w, 422, "invalid_expense_account")
			return
		}
		debits = []accountAmount{{ids[acctAP], total, ""}}
		credits = make([]accountAmount, 0, len(lines)+1)
		for _, l := range lines {
			credits = append(credits, accountAmount{l.AccountID, l.NetCents, ""})
		}
		if gst > 0 {
			credits = append(credits, accountAmount{ids[acctGST], gst, ""})
		}
	} else {
		if accountID == 0 {
			writeError(w, 422, "invalid_expense_account")
			return
		}
		debits = []accountAmount{{ids[acctAP], total, ""}}
		credits = []accountAmount{{accountID, net, ""}}
		if gst > 0 {
			credits = append(credits, accountAmount{ids[acctGST], gst, ""})
		}
	}
	key := "supplier-credit-approve:" + strconv.FormatInt(id, 10)
	hash := sha256.Sum256([]byte(key))
	var jid int64
	if currency != "SGD" {
		accounts, err := fxAccountIDs(ctx, tx, access.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		baseTotal := fxMulDivRound(total, fxRate, fxRateScale)
		baseCredits := scaleLegs(credits, baseTotal, total)
		legs := []fxLeg{{accounts[acctAP], baseTotal, currency}}
		for _, credit := range baseCredits {
			if credit.cents > 0 {
				legs = append(legs, fxLeg{credit.account, -credit.cents, currency})
			}
		}
		if billID > 0 {
			var billRate int64
			if err := tx.QueryRow(ctx, `SELECT fx_rate_micro FROM bills WHERE org_id=$1 AND id=$2`, access.ID, billID).Scan(&billRate); err != nil {
				serverError(w, err)
				return
			}
			billValue := fxMulDivRound(baseTotal, billRate, fxRate)
			diff := billValue - baseTotal
			if diff != 0 {
				legs = append(legs, realisedFXLeg(accounts, -diff, currency))
				legs[0].cents = billValue
			}
		}
		jid, err = postFXJournal(ctx, tx, access.ID, u.ID, creditDate, "Supplier credit "+reason, key, hash, legs)
	} else {
		jid, err = postJournalLegs(ctx, tx, access.ID, u.ID, creditDate, "Supplier credit "+reason, key, hash, debits, credits, map[int64]string{ids[acctGST]: "input"})
	}
	if err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE supplier_credits SET status='open',journal_id=$3 WHERE org_id=$1 AND id=$2`, access.ID, id, jid); err != nil {
		serverError(w, err)
		return
	}
	if err := recordAudit(ctx, tx, access.ID, u.ID, "supplier_credit", id, "supplier_credit.approved", map[string]any{"journal_id": jid}); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "open", "journal_id": jid, "total_cents": total})
}

// listSupplierCredits: GET /api/v1/supplier-credits — every credit, its own number and state.
func (a *app) listSupplierCredits(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT sc.id,sc.credit_number,sc.contact_id,c.name,sc.credit_date::text,sc.reason,
		sc.net_cents,sc.gst_cents,sc.total_cents,sc.status,sc.currency,sc.fx_rate_micro,COALESCE(sc.bill_id,0),COALESCE(b.document_number,'')
		FROM supplier_credits sc JOIN contacts c ON c.org_id=sc.org_id AND c.id=sc.contact_id
		LEFT JOIN bills b ON b.org_id=sc.org_id AND b.id=sc.bill_id
		WHERE sc.org_id=$1 ORDER BY sc.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type credit struct {
		ID           int64  `json:"id"`
		CreditNumber string `json:"credit_number"`
		ContactID    int64  `json:"contact_id"`
		SupplierName string `json:"supplier_name"`
		CreditDate   string `json:"credit_date"`
		Reference    string `json:"reference"`
		NetCents     int64  `json:"net_cents"`
		GSTCents     int64  `json:"gst_cents"`
		TotalCents   int64  `json:"total_cents"`
		Status       string `json:"status"`
		Currency     string `json:"currency"`
		FXRateMicro  int64  `json:"fx_rate_micro"`
		BillID       int64  `json:"bill_id"`
		BillNumber   string `json:"bill_number"`
	}
	out := []credit{}
	for rows.Next() {
		var c credit
		if err := rows.Scan(&c.ID, &c.CreditNumber, &c.ContactID, &c.SupplierName, &c.CreditDate, &c.Reference,
			&c.NetCents, &c.GSTCents, &c.TotalCents, &c.Status, &c.Currency, &c.FXRateMicro, &c.BillID, &c.BillNumber); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"supplier_credits": out})
}
