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

	"tidal/backend/internal/taxrules"
)

// Paid expenses record money already spent from bank. They post Dr expense / Cr bank in one journal.
// Nothing is sent to a bank; the entry only mirrors a payment the user already made.

type expenseItem struct {
	Description string `json:"description"`
	AccountID   int64  `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
	// GST is claimed per line (D6). AmountCents is the total paid, so net is amount minus tax.
	GSTCode      string `json:"gst_code,omitempty"`
	GSTRateBP    int64  `json:"gst_rate_bp,omitempty"`
	GSTCents     int64  `json:"gst_cents,omitempty"`
	BlockedInput bool   `json:"blocked_input,omitempty"`
}

type expenseInput struct {
	Custom               map[string]string `json:"custom"`
	Tags                 map[string]string `json:"tags"`
	Mileage              *mileageInput     `json:"mileage"`
	PaidThroughAccountID int64             `json:"paid_through_account_id"`
	VendorID             int64             `json:"vendor_id"`
	CustomerID           int64             `json:"customer_id"`
	ProjectID            int64             `json:"project_id"`
	Billable             bool              `json:"billable"`
	Items                []expenseItem     `json:"items"`
	IdempotencyKey       string            `json:"idempotency_key"`
	OrgID                int64             `json:"org_id"`
	ExpenseDate          string            `json:"expense_date"`
	Payee                string            `json:"payee"`
	Description          string            `json:"description"`
	ExpenseAccountID     int64             `json:"expense_account_id"`
	AmountCents          int64             `json:"amount_cents"`
	ReceiptReference     string            `json:"receipt_reference"`
	Notes                string            `json:"notes"`
	GSTCode              string            `json:"gst_code"`
	BlockedInput         bool              `json:"blocked_input"`
}

func validExpense(in *expenseInput) bool {
	in.Payee = strings.TrimSpace(in.Payee)
	in.Description = strings.TrimSpace(in.Description)
	in.ReceiptReference = strings.TrimSpace(in.ReceiptReference)
	if in.PaidThroughAccountID < 0 || in.VendorID < 0 || in.CustomerID < 0 || in.ProjectID < 0 || (in.Billable && in.CustomerID == 0) || len(in.Items) > 200 || !validTaxText(in.IdempotencyKey, 100) {
		return false
	}
	if len(in.Items) > 0 {
		var total int64
		for i := range in.Items {
			item := &in.Items[i]
			item.Description = strings.TrimSpace(item.Description)
			if item.AccountID <= 0 || item.AmountCents <= 0 || item.AmountCents > maxMoneyCents || item.Description == "" || !validTaxText(item.Description, 200) {
				return false
			}
			total += item.AmountCents
			if total > maxMoneyCents {
				return false
			}
		}
		if in.AmountCents != total {
			return false
		}
		in.ExpenseAccountID = in.Items[0].AccountID
	}
	return in.ExpenseAccountID > 0 && in.AmountCents >= 1 && in.AmountCents <= maxMoneyCents &&
		validTaxText(in.Payee, 200) && in.Payee != "" && validTaxText(in.Description, 200) && in.Description != "" &&
		validTaxText(in.ReceiptReference, 100) && validTaxText(in.Notes, 4000)
}

func (a *app) createExpense(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in expenseInput
	if !decodeJSON(w, r, &in) {
		return
	}
	date, ok := parseDay(in.ExpenseDate)
	if !ok || (in.Mileage == nil && !validExpense(&in)) {
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
	mileageData := []byte("{}")
	if in.Mileage != nil {
		if len(in.Items) > 0 {
			writeError(w, 400, "invalid_input")
			return
		}
		var pref mileagePreferences
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT expense_account_id,unit,default_rate_cents,rates FROM mileage_preferences WHERE org_id=$1 FOR SHARE`, access.ID).Scan(&pref.ExpenseAccountID, &pref.Unit, &pref.DefaultRateCents, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 422, "mileage_preferences_required")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if err := json.Unmarshal(raw, &pref.Rates); err != nil {
			serverError(w, err)
			return
		}
		rate := pref.DefaultRateCents
		for _, r := range pref.Rates {
			if r.StartDate <= in.ExpenseDate {
				rate = r.RateCents
			}
		}
		amount, valid := mileageAmount(in.Mileage, rate)
		if !valid {
			writeError(w, 400, "invalid_input")
			return
		}
		in.AmountCents = amount
		in.ExpenseAccountID = pref.ExpenseAccountID
		if !validExpense(&in) {
			writeError(w, 400, "invalid_input")
			return
		}
		mileageData, err = json.Marshal(mileageRecord{mileageInput: *in.Mileage, Unit: pref.Unit, RateCents: rate})
		if err != nil {
			serverError(w, err)
			return
		}
	}
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.PaidThroughAccountID == 0 {
		in.PaidThroughAccountID = ids[acctBank]
	}
	items := in.Items
	if len(items) == 0 {
		items = []expenseItem{{Description: in.Description, AccountID: in.ExpenseAccountID, AmountCents: in.AmountCents, GSTCode: in.GSTCode, BlockedInput: in.BlockedInput}}
	}
	// GST per line (D6): AmountCents is the total paid, so the tax is inside it. Claimable only when the
	// company was registered on the expense date, and never for blocked categories such as private use.
	gstRegistered, err := gstRegisteredOn(ctx, tx, access.ID, day(date))
	if err != nil {
		serverError(w, err)
		return
	}
	rateBP := taxrules.GSTRateBP(date)
	for i := range items {
		code := strings.ToUpper(strings.TrimSpace(items[i].GSTCode))
		if code == "" {
			code = strings.ToUpper(strings.TrimSpace(in.GSTCode))
		}
		switch code {
		case "", "NONE":
			items[i].GSTCode, items[i].GSTRateBP, items[i].GSTCents = "NONE", 0, 0
		case "SR", "ZR", "ES", "OS":
			if !gstRegistered {
				writeError(w, http.StatusUnprocessableEntity, "unsupported_gst")
				return
			}
			items[i].GSTCode = code
			if code == "SR" {
				if items[i].BlockedInput || in.BlockedInput {
					writeError(w, http.StatusUnprocessableEntity, "blocked_input_gst")
					return
				}
				items[i].GSTRateBP = rateBP
				items[i].GSTCents = items[i].AmountCents * rateBP / (10000 + rateBP)
			}
		default:
			writeError(w, http.StatusUnprocessableEntity, "unsupported_gst")
			return
		}
	}
	for _, item := range append(append([]expenseItem{}, items...), expenseItem{AccountID: in.PaidThroughAccountID}) {
		var typ string
		err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, item.AccountID).Scan(&typ)
		wanted := "expense"
		if item.AmountCents == 0 {
			wanted = "asset"
		}
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != wanted) {
			writeError(w, 422, "invalid_expense_account")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	for _, contact := range []struct {
		id       int64
		excluded string
	}{{in.VendorID, "customer"}, {in.CustomerID, "supplier"}} {
		if contact.id == 0 {
			continue
		}
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, contact.id).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == contact.excluded) {
			writeError(w, 422, "invalid_contact")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if in.ProjectID != 0 {
		var cid int64
		err := tx.QueryRow(ctx, `SELECT contact_id FROM projects WHERE org_id=$1 AND id=$2 AND active`, access.ID, in.ProjectID).Scan(&cid)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && cid != in.CustomerID) {
			writeError(w, 422, "invalid_project")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	canonical := canonicalKeyForExpense(access.ID, in, date)
	hash := sha256.Sum256([]byte(canonical))
	key := fmt.Sprintf("expense:%x", hash)
	if in.IdempotencyKey != "" {
		key = "expense:" + in.IdempotencyKey
	}
	var oldHash []byte
	var previousID, previousJournal, previousAmount int64
	err = tx.QueryRow(ctx, `SELECT e.id,e.journal_id,e.amount_cents,j.request_hash FROM expenses e JOIN journals j ON j.org_id=e.org_id AND j.id=e.journal_id WHERE e.org_id=$1 AND j.idempotency_key=$2`, access.ID, key).Scan(&previousID, &previousJournal, &previousAmount, &oldHash)
	if err == nil {
		if string(oldHash) != string(hash[:]) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]any{"id": previousID, "journal_id": previousJournal, "amount_cents": previousAmount})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	id, jid, err := insertExpense(ctx, tx, access.ID, u.ID, date, in, items, key, hash[:], mileageData, 0, nil)
	if err != nil {
		if isPeriodClosed(err) {
			writeError(w, 422, "period_closed")
		} else {
			serverError(w, err)
		}
		return
	}
	custom, tags, status, code, validationErr := validateExtras(ctx, tx, access.ID, "expenses", in.Custom, in.Tags)
	if validationErr != nil {
		serverError(w, validationErr)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	if err := saveExtrasTx(ctx, tx, access.ID, "expenses", id, custom, tags); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'expense.created',jsonb_build_object('expense_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "journal_id": jid, "amount_cents": in.AmountCents})
}

// insertExpense posts one paid expense (journal, lines, expense row) inside the caller's transaction.
// recurringID and runDate tie a generated expense to its profile so one run date can never post twice.
func insertExpense(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, in expenseInput, items []expenseItem, key string, hash, mileageData []byte, recurringID int64, runDate any) (id, jid int64, err error) {
	if err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, orgID, date, "Paid expense: "+in.Payee+" - "+in.Description, key, hash, userID).Scan(&jid); err != nil {
		return
	}
	line := 0
	var gstTotal int64
	headerCode, headerRate := "NONE", int64(0)
	for _, item := range items {
		net := item.AmountCents - item.GSTCents
		if net > 0 {
			line++
			if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,0)`, orgID, jid, line, item.AccountID, net); err != nil {
				return
			}
		}
		gstTotal += item.GSTCents
		if item.GSTCode != "" && item.GSTCode != "NONE" {
			headerCode, headerRate = item.GSTCode, item.GSTRateBP
		}
	}
	if gstTotal > 0 {
		ids, aerr := accountIDs(ctx, tx, orgID)
		if aerr != nil {
			return 0, 0, aerr
		}
		line++
		if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tax_direction) VALUES($1,$2,$3,$4,$5,0,'input')`, orgID, jid, line, ids[acctGST], gstTotal); err != nil {
			return
		}
	}
	line++
	if _, err = tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,0,$5)`, orgID, jid, line, in.PaidThroughAccountID, in.AmountCents); err != nil {
		return
	}
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO expenses (org_id, expense_date, payee, description, expense_account_id, amount_cents, receipt_reference, notes, journal_id, created_by,paid_through_account_id,vendor_id,customer_id,items,mileage,recurring_profile_id,recurring_run_date,project_id,billable,gst_code,gst_rate_bp,gst_cents)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,0),NULLIF($13,0),$14,$15,NULLIF($16,0),$17,NULLIF($18,0),$19,$20,$21,$22) RETURNING id`,
		orgID, date, in.Payee, in.Description, in.ExpenseAccountID, in.AmountCents, in.ReceiptReference, in.Notes, jid, userID, in.PaidThroughAccountID, in.VendorID, in.CustomerID, data, mileageData, recurringID, runDate, in.ProjectID, in.Billable, headerCode, headerRate, gstTotal).Scan(&id)
	return
}

// canonicalKeyForExpense makes a retry-safe key from the request contents, so a repeated submit
// with identical data cannot record the same expense twice.
func canonicalKeyForExpense(orgID int64, in expenseInput, date time.Time) string {
	b, _ := json.Marshal([]any{orgID, day(date), in.Payee, in.Description, in.ExpenseAccountID, in.AmountCents, in.ReceiptReference, in.Notes, in.PaidThroughAccountID, in.VendorID, in.CustomerID, in.Items, in.Mileage, in.ProjectID, in.Billable, in.Custom, in.Tags, in.GSTCode, in.BlockedInput})
	return string(b)
}

func (a *app) listExpenses(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT e.id, e.expense_date::text, e.payee, e.description, e.expense_account_id, a.code, a.name, e.amount_cents, e.receipt_reference, e.notes, e.journal_id,COALESCE(e.paid_through_account_id,0),COALESCE(e.vendor_id,0),COALESCE(e.customer_id,0),e.items,e.mileage,COALESCE(e.project_id,0),e.billable,COALESCE(l.invoice_id,0)
		 FROM expenses e JOIN accounts a ON a.org_id=e.org_id AND a.id=e.expense_account_id
		 LEFT JOIN expense_invoice_links l ON l.org_id=e.org_id AND l.expense_id=e.id
		 WHERE e.org_id=$1 ORDER BY e.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type expense struct {
		PaidThroughAccountID int64           `json:"paid_through_account_id"`
		VendorID             int64           `json:"vendor_id"`
		CustomerID           int64           `json:"customer_id"`
		ProjectID            int64           `json:"project_id"`
		Billable             bool            `json:"billable"`
		InvoiceID            int64           `json:"invoice_id"`
		Mileage              json.RawMessage `json:"mileage"`
		Items                json.RawMessage `json:"items"`
		ID                   int64           `json:"id"`
		ExpenseDate          string          `json:"expense_date"`
		Payee                string          `json:"payee"`
		Description          string          `json:"description"`
		ExpenseAccountID     int64           `json:"expense_account_id"`
		AccountCode          string          `json:"account_code"`
		AccountName          string          `json:"account_name"`
		AmountCents          int64           `json:"amount_cents"`
		ReceiptReference     string          `json:"receipt_reference"`
		Notes                string          `json:"notes"`
		JournalID            int64           `json:"journal_id"`
	}
	out := []expense{}
	for rows.Next() {
		var e expense
		if err := rows.Scan(&e.ID, &e.ExpenseDate, &e.Payee, &e.Description, &e.ExpenseAccountID, &e.AccountCode, &e.AccountName, &e.AmountCents, &e.ReceiptReference, &e.Notes, &e.JournalID, &e.PaidThroughAccountID, &e.VendorID, &e.CustomerID, &e.Items, &e.Mileage, &e.ProjectID, &e.Billable, &e.InvoiceID); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expenses": out})
}
