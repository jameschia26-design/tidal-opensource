package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Suggestions are the documents already recorded here that could explain a statement line. They are
// ranked so the most likely match is first: same day before a nearby day, then the closest amount
// date. Matching is always an explicit click; nothing is matched or posted automatically.

type bankSuggestion struct {
	Kind        string `json:"kind"`
	ID          int64  `json:"id"`
	AmountCents int64  `json:"amount_cents"`
	Date        string `json:"date"`
	Label       string `json:"label"`
	DaysApart   int    `json:"days_apart"`
}

// suggestStatementLine: GET /api/v1/bank/lines/{id}/suggestions — existing payments, receipts,
// expenses and transfers with the line's amount inside the company's match tolerance.
func (a *app) suggestStatementLine(w http.ResponseWriter, r *http.Request, u currentUser) {
	lineID, ok := pathID(w, r)
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
	ctx := r.Context()
	var bankID *int64
	var lineDate time.Time
	var amount int64
	if err := a.db.QueryRow(ctx, `SELECT bank_account_id, line_date, amount_cents FROM bank_statement_lines
		WHERE org_id=$1 AND id=$2`, access.ID, lineID).Scan(&bankID, &lineDate, &amount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	var tolerance int
	if err := a.db.QueryRow(ctx, `SELECT bank_match_tolerance_days FROM organizations WHERE id=$1`, access.ID).Scan(&tolerance); err != nil {
		serverError(w, err)
		return
	}
	abs := amount
	if abs < 0 {
		abs = -abs
	}
	type raw struct {
		suggestion bankSuggestion
		date       time.Time
	}
	out := []raw{}
	add := func(kind string, id int64, amount int64, day time.Time, label string) {
		days := int(day.Sub(lineDate).Hours() / 24)
		if days < 0 {
			days = -days
		}
		out = append(out, raw{bankSuggestion{Kind: kind, ID: id, AmountCents: amount, Date: day.Format("2006-01-02"), Label: label, DaysApart: days}, day})
	}
	// Money in: invoice payments and sales receipts. Money out: bill payments and expenses.
	if amount > 0 {
		rows, err := a.db.Query(ctx, `SELECT p.id,p.amount_cents,p.payment_date,
			'Invoice ' || COALESCE(i.number::text, i.id::text) || ' payment' || COALESCE(' — ' || c.name, '')
			FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id
			LEFT JOIN contacts c ON c.org_id=i.org_id AND c.id=i.contact_id
			WHERE p.org_id=$1 AND p.amount_cents=$2 AND p.payment_date BETWEEN $3::date-$4::int AND $3::date+$4::int
			  AND NOT EXISTS (SELECT 1 FROM bank_line_matches m WHERE m.org_id=p.org_id AND m.doc_kind='invoice' AND m.doc_id=p.id)`,
			access.ID, abs, lineDate, tolerance)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var id, cents int64
			var day time.Time
			var label string
			if err := rows.Scan(&id, &cents, &day, &label); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			add("invoice", id, cents, day, label)
		}
		rows.Close()
		rows, err = a.db.Query(ctx, `SELECT sr.id,sr.total_cents,sr.receipt_date,
			'Sales receipt ' || sr.receipt_number || COALESCE(' — ' || c.name, '')
			FROM sales_receipts sr LEFT JOIN contacts c ON c.org_id=sr.org_id AND c.id=sr.contact_id
			WHERE sr.org_id=$1 AND sr.total_cents=$2 AND sr.receipt_date BETWEEN $3::date-$4::int AND $3::date+$4::int
			  AND NOT EXISTS (SELECT 1 FROM bank_line_matches m WHERE m.org_id=sr.org_id AND m.doc_kind='receipt' AND m.doc_id=sr.id)`,
			access.ID, abs, lineDate, tolerance)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var id, cents int64
			var day time.Time
			var label string
			if err := rows.Scan(&id, &cents, &day, &label); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			add("receipt", id, cents, day, label)
		}
		rows.Close()
	} else {
		rows, err := a.db.Query(ctx, `SELECT bp.id,bp.amount_cents,bp.payment_date,
			'Bill ' || COALESCE(NULLIF(b.supplier_reference,''), b.id::text) || ' payment' || COALESCE(' — ' || c.name, '')
			FROM bill_payments bp JOIN bills b ON b.org_id=bp.org_id AND b.id=bp.bill_id
			LEFT JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id
			WHERE bp.org_id=$1 AND bp.amount_cents=$2 AND bp.payment_date BETWEEN $3::date-$4::int AND $3::date+$4::int
			  AND NOT EXISTS (SELECT 1 FROM bank_line_matches m WHERE m.org_id=bp.org_id AND m.doc_kind='bill' AND m.doc_id=bp.id)`,
			access.ID, abs, lineDate, tolerance)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var id, cents int64
			var day time.Time
			var label string
			if err := rows.Scan(&id, &cents, &day, &label); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			add("bill", id, cents, day, label)
		}
		rows.Close()
		rows, err = a.db.Query(ctx, `SELECT e.id,e.amount_cents,e.expense_date,'Expense — ' || e.payee
			FROM expenses e
			WHERE e.org_id=$1 AND e.amount_cents=$2 AND e.expense_date BETWEEN $3::date-$4::int AND $3::date+$4::int
			  AND NOT EXISTS (SELECT 1 FROM bank_line_matches m WHERE m.org_id=e.org_id AND m.doc_kind='expense' AND m.doc_id=e.id)`,
			access.ID, abs, lineDate, tolerance)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var id, cents int64
			var day time.Time
			var label string
			if err := rows.Scan(&id, &cents, &day, &label); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			add("expense", id, cents, day, label)
		}
		rows.Close()
	}
	// Transfers are journals that move money between two accounts; whichever side this statement sees,
	// the journal is a candidate.
	if bankID != nil {
		rows, err := a.db.Query(ctx, `SELECT j.id, abs(SUM(jl.debit_cents-jl.credit_cents)), j.entry_date,
			'Transfer ' || j.memo
			FROM journals j JOIN journal_lines jl ON jl.org_id=j.org_id AND jl.journal_id=j.id AND jl.account_id=$2
			WHERE j.org_id=$1 AND j.idempotency_key LIKE 'transfer:%'
			  AND j.entry_date BETWEEN $3::date-$4::int AND $3::date+$4::int
			  AND NOT EXISTS (SELECT 1 FROM bank_line_matches m WHERE m.org_id=j.org_id AND m.doc_kind='transfer' AND m.doc_id=j.id)
			GROUP BY j.id, j.entry_date, j.memo HAVING abs(SUM(jl.debit_cents-jl.credit_cents))=$5`,
			access.ID, *bankID, lineDate, tolerance, abs)
		if err != nil {
			serverError(w, err)
			return
		}
		for rows.Next() {
			var id, cents int64
			var day time.Time
			var label string
			if err := rows.Scan(&id, &cents, &day, &label); err != nil {
				rows.Close()
				serverError(w, err)
				return
			}
			if (amount > 0) != (cents > 0) {
				continue
			}
			add("transfer", id, abs, day, label)
		}
		rows.Close()
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].suggestion.DaysApart != out[j].suggestion.DaysApart {
			return out[i].suggestion.DaysApart < out[j].suggestion.DaysApart
		}
		return out[i].date.After(out[j].date)
	})
	suggestions := make([]bankSuggestion, 0, len(out))
	for i, s := range out {
		if i == 50 {
			break
		}
		suggestions = append(suggestions, s.suggestion)
	}
	writeJSON(w, http.StatusOK, map[string]any{"line_id": lineID, "suggestions": suggestions})
}

// createFromStatementLine: POST /api/v1/bank/lines/{id}/create — spend money, receive money or transfer
// straight from a statement line, then match that line to the new entry. The amount comes from the
// statement, so the line always reconciles; the user only chooses the other account.
func (a *app) createFromStatementLine(w http.ResponseWriter, r *http.Request, u currentUser) {
	lineID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID     int64            `json:"org_id"`
		Kind      string           `json:"kind"`
		AccountID int64            `json:"account_id"`
		Memo      string           `json:"memo"`
		Tracking  map[string]int64 `json:"tracking"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Memo = strings.TrimSpace(in.Memo)
	if !validStage(in.Kind, "spend", "receive", "transfer") || in.AccountID <= 0 || !validTaxText(in.Memo, 200) || len(in.Tracking) > 2 {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	var bankID *int64
	var lineDate time.Time
	var amount int64
	var kind *string
	if err := tx.QueryRow(ctx, `SELECT bank_account_id, line_date, amount_cents, matched_payment_kind
		FROM bank_statement_lines WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, lineID).Scan(&bankID, &lineDate, &amount, &kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if bankID == nil {
		writeError(w, http.StatusConflict, "statement_account_missing")
		return
	}
	if kind != nil {
		writeError(w, http.StatusConflict, "statement_line_matched")
		return
	}
	// The chosen account must belong to this company. A transfer must land on a different bank account
	// on the other side of the line.
	var acctType string
	var acctIsBank bool
	if err := tx.QueryRow(ctx, `SELECT type, COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.AccountID).Scan(&acctType, &acctIsBank); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_account")
			return
		}
		serverError(w, err)
		return
	}
	if in.Kind == "transfer" {
		if !acctIsBank || in.AccountID == *bankID {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
			return
		}
	} else if acctType != "expense" && acctType != "revenue" && acctType != "asset" && acctType != "liability" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_account")
		return
	}
	trackOK, trackErr := validTracking(ctx, tx, access.ID, in.Tracking)
	if trackErr != nil {
		serverError(w, trackErr)
		return
	}
	if !trackOK {
		writeError(w, http.StatusUnprocessableEntity, "invalid_tracking")
		return
	}
	if in.Memo == "" {
		in.Memo = "Bank line"
	}
	abs := amount
	if abs < 0 {
		abs = -abs
	}
	trackingJSON := ""
	if len(in.Tracking) > 0 {
		raw, _ := json.Marshal(in.Tracking)
		trackingJSON = string(raw)
	}
	// A statement line is matched once, so its journal key is fixed and a retry cannot post twice.
	key := "bankline:" + strconv.FormatInt(lineID, 10)
	docKind := "journal"
	var debit, credit accountAmount
	switch {
	case in.Kind == "spend" && amount < 0, in.Kind == "receive" && amount > 0:
		if in.Kind == "spend" {
			debit = accountAmount{in.AccountID, abs, trackingJSON}
			credit = accountAmount{*bankID, abs, ""}
		} else {
			debit = accountAmount{*bankID, abs, ""}
			credit = accountAmount{in.AccountID, abs, trackingJSON}
		}
	case in.Kind == "transfer":
		key = "transfer:" + key
		docKind = "transfer"
		if amount < 0 {
			debit = accountAmount{in.AccountID, abs, trackingJSON}
			credit = accountAmount{*bankID, abs, ""}
		} else {
			debit = accountAmount{*bankID, abs, ""}
			credit = accountAmount{in.AccountID, abs, trackingJSON}
		}
	default:
		writeError(w, http.StatusUnprocessableEntity, "match_direction")
		return
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%d|%d|%s", lineID, in.Kind, in.AccountID, abs, trackingJSON)))
	journalID, err := postJournalLegs(ctx, tx, access.ID, u.ID, lineDate, in.Memo, key, sum,
		[]accountAmount{debit}, []accountAmount{credit})
	if isPeriodClosed(err) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bank_line_matches (org_id, line_id, doc_kind, doc_id, amount_cents, created_by)
		VALUES ($1,$2,$3,$4,$5,$6)`, access.ID, lineID, docKind, journalID, amount, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE bank_statement_lines SET matched_payment_kind=$3, matched_payment_id=$4
		WHERE org_id=$1 AND id=$2`, access.ID, lineID, docKind, journalID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail)
		VALUES($1,$2,'bank.line_created',jsonb_build_object('line_id',$3::bigint,'kind',$4::text,'journal_id',$5::bigint))`,
		access.ID, u.ID, lineID, in.Kind, journalID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"journal_id": journalID, "line_id": lineID,
		"matched_kind": docKind, "matched_id": journalID, "amount_cents": amount})
}
