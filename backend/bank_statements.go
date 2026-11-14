package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Bank statements are imported from a CSV file the user downloads from their bank.
// Format: date (YYYY-MM-DD), description, reference, amount in SGD (negative for money out).
// Tidal never connects to a bank and never moves money.

const maxStatementRows = 2000

type statementRow struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Reference   string `json:"reference"`
	Amount      string `json:"amount"`
}

func (a *app) importStatement(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	// Statements belong to one bank account (R16). Default to the seeded bank account.
	bankID, ok := parseAccountID(r.URL.Query().Get("bank_account_id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	// Column mapping: each field is a 1-based column in the file, defaulting to the template order.
	col := func(name string, def int) (int, bool) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return def, true
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 20 {
			return 0, false
		}
		return n, true
	}
	dateCol, ok1 := col("date_col", 1)
	descCol, ok2 := col("description_col", 2)
	refCol, ok3 := col("reference_col", 3)
	amountCol, ok4 := col("amount_col", 4)
	if !ok1 || !ok2 || !ok3 || !ok4 ||
		dateCol == descCol || dateCol == refCol || dateCol == amountCol ||
		descCol == refCol || descCol == amountCol || refCol == amountCol {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	fields := dateCol
	for _, n := range []int{descCol, refCol, amountCol} {
		if n > fields {
			fields = n
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	rd := csv.NewReader(r.Body)
	rd.FieldsPerRecord = fields
	rd.TrimLeadingSpace = true
	records, err := rd.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_statement")
		return
	}
	if len(records) > maxStatementRows {
		writeError(w, http.StatusBadRequest, "statement_too_large")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Seeded accounts may not exist yet for a brand-new company.
	if _, err := accountIDs(ctx, tx, access.ID); err != nil {
		serverError(w, err)
		return
	}
	if bankID == 0 {
		if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, access.ID, acctBank).Scan(&bankID); err != nil {
			serverError(w, err)
			return
		}
	}
	var bankOK bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, bankID).Scan(&bankOK); err != nil || !bankOK {
		if errors.Is(err, pgx.ErrNoRows) || err == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
		} else {
			serverError(w, err)
		}
		return
	}
	imported, duplicates := 0, 0
	for i, rec := range records {
		if i == 0 && strings.EqualFold(strings.TrimSpace(rec[dateCol-1]), "date") {
			continue
		}
		date, dok := parseDay(strings.TrimSpace(rec[dateCol-1]))
		desc := strings.TrimSpace(rec[descCol-1])
		ref := strings.TrimSpace(rec[refCol-1])
		amount, aok := parseStatementCents(strings.TrimSpace(rec[amountCol-1]))
		if !dok || !aok || amount == 0 || desc == "" || !validTaxText(desc, 300) || !validTaxText(ref, 100) {
			writeError(w, http.StatusBadRequest, "invalid_statement")
			return
		}
		key := importKey(bankID, day(date), ref, amount)
		tag, err := tx.Exec(ctx, `INSERT INTO bank_statement_lines (org_id,line_date,description,reference,amount_cents,import_key,created_by,bank_account_id)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (org_id, bank_account_id, import_key) DO NOTHING`,
			access.ID, date, desc, ref, amount, key, u.ID, bankID)
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			duplicates++
		} else {
			imported++
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bank.imported',jsonb_build_object('imported',$3::int,'duplicates',$4::int))`, access.ID, u.ID, imported, duplicates); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "duplicates_skipped": duplicates, "bank_account_id": bankID})
}

// parseStatementCents reads "-1234.5" or "12" as integer cents. Only digits, an optional minus and at most two decimals.
func parseStatementCents(s string) (int64, bool) {
	neg := strings.HasPrefix(s, "-")
	t := strings.TrimPrefix(s, "-")
	parts := strings.Split(t, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts[0]) > 12 {
		return 0, false
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, false
	}
	var frac int64
	if len(parts) == 2 {
		f := parts[1]
		if len(f) == 1 {
			f += "0"
		}
		if len(f) != 2 {
			return 0, false
		}
		if frac, err = strconv.ParseInt(f, 10, 64); err != nil {
			return 0, false
		}
	}
	v := whole*100 + frac
	if neg {
		v = -v
	}
	return v, true
}

// importKey deduplicates a statement line by the account it arrived on plus date, amount and
// reference — the fields a bank statement can repeat identically. The description is left out because
// a bank can change its wording between downloads of the same movement.
func importKey(bankID int64, date, ref string, amount int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%d|%s", bankID, date, amount, ref)))
	return hex.EncodeToString(sum[:])
}

// bankLine is one imported statement line plus the rule suggestion attached to it. A suggestion is
// advice: the line stays unmatched until a person matches or creates something.
type bankLine struct {
	ID            int64   `json:"id"`
	BankAccountID *int64  `json:"bank_account_id"`
	Date          string  `json:"date"`
	Description   string  `json:"description"`
	Reference     string  `json:"reference"`
	AmountCents   int64   `json:"amount_cents"`
	MatchedKind   *string `json:"matched_kind"`
	MatchedID     *int64  `json:"matched_id"`
	Status        string  `json:"status"`
	// SuggestedAccountID keeps the older clients (and the statement table) reading one field; the
	// full suggestion carries the rule's contact, tax and tracking actions too.
	SuggestedAccountID *int64              `json:"suggested_account_id"`
	Suggestion         *bankRuleSuggestion `json:"suggestion"`
}

func (a *app) listStatementLines(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	orgID, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	bankID, ok := parseAccountID(q.Get("bank_account_id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	rules, err := a.loadBankRules(r.Context(), access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT l.id, l.bank_account_id, l.line_date::text, l.description,
		l.reference, l.amount_cents, l.matched_payment_kind, l.matched_payment_id
		FROM bank_statement_lines l
		WHERE l.org_id=$1 AND ($2=0 OR l.bank_account_id=$2)
		ORDER BY l.line_date DESC, l.id DESC LIMIT 500`, access.ID, bankID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []bankLine{}
	for rows.Next() {
		var l bankLine
		if err := rows.Scan(&l.ID, &l.BankAccountID, &l.Date, &l.Description, &l.Reference,
			&l.AmountCents, &l.MatchedKind, &l.MatchedID); err != nil {
			serverError(w, err)
			return
		}
		if l.MatchedKind == nil {
			l.Status = "unmatched"
		} else {
			l.Status = "matched"
		}
		l.Suggestion = matchBankRule(rules, l)
		if l.Suggestion != nil {
			id := l.Suggestion.AccountID
			l.SuggestedAccountID = &id
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out})
}

// matchStatementLine links a statement line to a payment, bill payment, expense, sales receipt or transfer.
// One line can cover several documents (a grouped deposit), and each document matches once. The line is only
// marked matched once the matched total equals the line amount (R16).
func (a *app) matchStatementLine(w http.ResponseWriter, r *http.Request, u currentUser) {
	lineID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID     int64  `json:"org_id"`
		Kind      string `json:"kind"`
		PaymentID int64  `json:"payment_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validStage(in.Kind, "invoice", "bill", "expense", "receipt", "transfer", "journal") || in.PaymentID <= 0 {
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
	var bankID *int64
	var lineDate time.Time
	var amount int64
	if err := tx.QueryRow(ctx, `SELECT bank_account_id, line_date, amount_cents FROM bank_statement_lines WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, lineID).Scan(&bankID, &lineDate, &amount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if bankID == nil {
		writeError(w, http.StatusConflict, "statement_account_missing")
		return
	}
	// Resolve the document to the journal that moved money, so every cash type validates the same way.
	var journalID int64
	switch in.Kind {
	case "invoice":
		err = tx.QueryRow(ctx, `SELECT journal_id FROM payments WHERE org_id=$1 AND id=$2`, access.ID, in.PaymentID).Scan(&journalID)
	case "bill":
		err = tx.QueryRow(ctx, `SELECT journal_id FROM bill_payments WHERE org_id=$1 AND id=$2`, access.ID, in.PaymentID).Scan(&journalID)
	case "expense":
		err = tx.QueryRow(ctx, `SELECT journal_id FROM expenses WHERE org_id=$1 AND id=$2`, access.ID, in.PaymentID).Scan(&journalID)
	case "receipt":
		err = tx.QueryRow(ctx, `SELECT journal_id FROM sales_receipts WHERE org_id=$1 AND id=$2`, access.ID, in.PaymentID).Scan(&journalID)
	case "transfer":
		err = tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND id=$2 AND idempotency_key LIKE 'transfer:%'`, access.ID, in.PaymentID).Scan(&journalID)
	case "journal":
		err = tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND id=$2`, access.ID, in.PaymentID).Scan(&journalID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var journalDate time.Time
	var effect int64
	if err := tx.QueryRow(ctx, `SELECT j.entry_date, COALESCE(SUM(jl.debit_cents-jl.credit_cents),0)
		FROM journals j LEFT JOIN journal_lines jl ON jl.org_id=j.org_id AND jl.journal_id=j.id AND jl.account_id=$3
		WHERE j.org_id=$1 AND j.id=$2 GROUP BY j.entry_date`, access.ID, journalID, *bankID).Scan(&journalDate, &effect); err != nil {
		serverError(w, err)
		return
	}
	var tolerance int
	if err := tx.QueryRow(ctx, `SELECT bank_match_tolerance_days FROM organizations WHERE id=$1`, access.ID).Scan(&tolerance); err != nil {
		serverError(w, err)
		return
	}
	days := int(journalDate.Sub(lineDate).Hours() / 24)
	if days < 0 {
		days = -days
	}
	if days > tolerance {
		writeError(w, http.StatusUnprocessableEntity, "match_date_mismatch")
		return
	}
	if effect == 0 || (effect > 0) != (amount > 0) {
		writeError(w, http.StatusUnprocessableEntity, "match_direction")
		return
	}
	var already, total int64
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE(SUM(amount_cents),0),
		COALESCE(SUM(amount_cents) FILTER (WHERE doc_kind=$3 AND doc_id=$4),0)
		FROM bank_line_matches WHERE org_id=$1 AND line_id=$2`, access.ID, lineID, in.Kind, in.PaymentID).Scan(&total, &already); err != nil {
		serverError(w, err)
		return
	}
	if already != 0 {
		writeError(w, http.StatusConflict, "payment_already_matched")
		return
	}
	if total+effect > amount && amount > 0 || total+effect < amount && amount < 0 {
		writeError(w, http.StatusUnprocessableEntity, "match_exceeds")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bank_line_matches(org_id,line_id,doc_kind,doc_id,amount_cents,created_by) VALUES($1,$2,$3,$4,$5,$6)`,
		access.ID, lineID, in.Kind, in.PaymentID, effect, u.ID); err != nil {
		serverError(w, err)
		return
	}
	// Legacy columns keep single-document matches visible to older callers.
	total += effect
	if total == amount {
		if _, err := tx.Exec(ctx, `UPDATE bank_statement_lines SET matched_payment_kind=$3, matched_payment_id=$4 WHERE org_id=$1 AND id=$2`,
			access.ID, lineID, in.Kind, in.PaymentID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bank.matched',jsonb_build_object('line_id',$3::bigint,'kind',$4::text,'doc_id',$5::bigint))`,
		access.ID, u.ID, lineID, in.Kind, in.PaymentID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": lineID, "matched_kind": in.Kind, "matched_id": in.PaymentID, "matched_cents": total, "line_cents": amount})
}

// defaultBankAccount returns the seeded bank account for the org.
func (a *app) defaultBankAccount(w http.ResponseWriter, ctx context.Context, orgID int64) (int64, bool) {
	var id int64
	if err := a.db.QueryRow(ctx, `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, orgID, acctBank).Scan(&id); err != nil {
		serverError(w, err)
		return 0, false
	}
	return id, true
}

// isBankAccount refuses accounts not flagged as a bank account.
func (a *app) isBankAccount(w http.ResponseWriter, ctx context.Context, orgID, accountID int64) bool {
	var bank bool
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, orgID, accountID).Scan(&bank); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
			return false
		}
		serverError(w, err)
		return false
	}
	if !bank {
		writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
		return false
	}
	return true
}

// parseAccountID reads an optional positive account id.
func parseAccountID(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// unmatchStatementLine: POST /api/v1/bank/lines/{id}/unmatch — frees a matched statement line so the payment
// can be corrected. Lines dated in a closed period stay matched, and every unmatch is audited (R15/R16).
func (a *app) unmatchStatementLine(w http.ResponseWriter, r *http.Request, u currentUser) {
	lineID, ok := pathID(w, r)
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
	var lineDate time.Time
	var kind *string
	var pid *int64
	if err := tx.QueryRow(ctx, `SELECT line_date, matched_payment_kind, matched_payment_id FROM bank_statement_lines WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, lineID).Scan(&lineDate, &kind, &pid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if kind == nil || pid == nil {
		writeError(w, http.StatusConflict, "statement_line_unmatched")
		return
	}
	var closed *time.Time
	if err := tx.QueryRow(ctx, `SELECT closed_through FROM period_closes WHERE org_id=$1`, access.ID).Scan(&closed); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if closed != nil && !lineDate.After(*closed) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bank_line_matches WHERE org_id=$1 AND line_id=$2`, access.ID, lineID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE bank_statement_lines SET matched_payment_kind=NULL, matched_payment_id=NULL WHERE org_id=$1 AND id=$2`, access.ID, lineID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bank.unmatched',jsonb_build_object('line_id',$3::bigint,'payment_kind',$4::text,'payment_id',$5::bigint))`,
		access.ID, u.ID, lineID, *kind, *pid); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": lineID, "matched_kind": nil, "matched_id": nil})
}
