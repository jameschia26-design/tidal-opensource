package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// A bank account is a chart account flagged as a bank account, plus the details the bank accounts
// screen shows. Everything that posts to it (payments, expenses, transfers) keeps using the same
// ledger account, so balances never come from two sources.

type bankAccount struct {
	ID                    int64  `json:"id"`
	Code                  string `json:"code"`
	Name                  string `json:"name"`
	AccountNumber         string `json:"account_number"`
	AccountType           string `json:"account_type"`
	OpeningBalanceCents   int64  `json:"opening_balance_cents"`
	OpeningBalanceDate    string `json:"opening_balance_date"`
	StatementBalanceCents int64  `json:"statement_balance_cents"`
	ImportedBalanceCents  int64  `json:"imported_balance_cents"`
	LedgerBalanceCents    int64  `json:"ledger_balance_cents"`
	Currency              string `json:"currency"`
	Archived              bool   `json:"archived"`
	LastReconciledTo      string `json:"last_reconciled_to"`
}

type bankAccountInput struct {
	OrgID                 int64  `json:"org_id"`
	Code                  string `json:"code"`
	Name                  string `json:"name"`
	AccountNumber         string `json:"account_number"`
	AccountType           string `json:"account_type"`
	OpeningBalanceCents   int64  `json:"opening_balance_cents"`
	OpeningBalanceDate    string `json:"opening_balance_date"`
	StatementBalanceCents int64  `json:"statement_balance_cents"`
	Currency              string `json:"currency"`
	Archived              bool   `json:"archived"`
}

func validBankAccount(in *bankAccountInput, requireCode bool) bool {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.AccountNumber = strings.TrimSpace(in.AccountNumber)
	in.OpeningBalanceDate = strings.TrimSpace(in.OpeningBalanceDate)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "SGD"
	}
	if in.Name == "" || !validTaxText(in.Name, 200) || !validTaxText(in.AccountNumber, 50) {
		return false
	}
	if requireCode && (in.Code == "" || len(in.Code) > 20) {
		return false
	}
	if in.Code != "" && !validTaxText(in.Code, 20) {
		return false
	}
	if !validStage(in.AccountType, "bank", "credit_card", "paypal", "other") || in.Currency != "SGD" {
		return false
	}
	if in.OpeningBalanceCents < -maxMoneyCents || in.OpeningBalanceCents > maxMoneyCents ||
		in.StatementBalanceCents < -maxMoneyCents || in.StatementBalanceCents > maxMoneyCents {
		return false
	}
	if in.OpeningBalanceDate != "" {
		if _, ok := parseDay(in.OpeningBalanceDate); !ok {
			return false
		}
	}
	return true
}

// listBankAccounts: GET /api/v1/bank/accounts — every flagged bank account with the three balances the
// screen shows: opening plus imported statement lines, the ledger ("Balance in Xero") and the stored
// statement balance last confirmed by a reconciliation.
func (a *app) listBankAccounts(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.id, a.code, a.name, a.bank_account_number, a.bank_account_type,
		a.bank_opening_balance_cents, COALESCE(a.bank_opening_balance_date::text, ''),
		a.bank_statement_balance_cents, a.bank_currency, a.archived,
		a.bank_opening_balance_cents + COALESCE((SELECT SUM(l.amount_cents) FROM bank_statement_lines l
			WHERE l.org_id=a.org_id AND l.bank_account_id=a.id), 0),
		COALESCE((SELECT SUM(jl.debit_cents-jl.credit_cents) FROM journal_lines jl
			JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
			WHERE jl.org_id=a.org_id AND jl.account_id=a.id), 0),
		COALESCE((SELECT max(rc.period_to)::text FROM bank_reconciliations rc
			WHERE rc.org_id=a.org_id AND rc.bank_account_id=a.id), '')
		FROM accounts a WHERE a.org_id=$1 AND a.is_bank_account ORDER BY a.archived, a.code`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []bankAccount{}
	for rows.Next() {
		var b bankAccount
		if err := rows.Scan(&b.ID, &b.Code, &b.Name, &b.AccountNumber, &b.AccountType,
			&b.OpeningBalanceCents, &b.OpeningBalanceDate, &b.StatementBalanceCents, &b.Currency, &b.Archived,
			&b.ImportedBalanceCents, &b.LedgerBalanceCents, &b.LastReconciledTo); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// nextBankCode picks the first free code above the seeded bank account so "Add bank account" never
// asks the user to invent one.
func nextBankCode(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID int64) (string, error) {
	reserved := map[string]bool{}
	for _, s := range seedAccounts {
		reserved[s.code] = true
	}
	for n := 1000; n < 2000; n++ {
		code := strconv.Itoa(n)
		if reserved[code] {
			continue
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND code=$2)`, orgID, code).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
	return "", errors.New("no free bank account code")
}

// createBankAccount: POST /api/v1/bank/accounts — a general ledger asset account flagged as a bank
// account, with its statement details. It is offered to statements, rules and payments immediately.
func (a *app) createBankAccount(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in bankAccountInput
	if !decodeJSON(w, r, &in) || !validBankAccount(&in, false) {
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
	code := in.Code
	if code == "" {
		if code, err = nextBankCode(ctx, tx, access.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	for _, s := range seedAccounts {
		if code == s.code {
			writeError(w, http.StatusConflict, "reserved_account")
			return
		}
	}
	var openDate any
	if in.OpeningBalanceDate != "" {
		openDate = in.OpeningBalanceDate
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO accounts (org_id, code, name, type, class, is_bank_account,
		bank_account_number, bank_account_type, bank_opening_balance_cents, bank_opening_balance_date,
		bank_statement_balance_cents, bank_currency)
		VALUES ($1,$2,$3,'asset','bank',true,$4,$5,$6,$7,$8,$9) RETURNING id`,
		access.ID, code, in.Name, in.AccountNumber, in.AccountType, in.OpeningBalanceCents, openDate,
		in.StatementBalanceCents, in.Currency).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "account_code_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bank.account_created',jsonb_build_object('account_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "code": code})
}

// updateBankAccount: PUT /api/v1/bank/accounts/{id} — edits the statement details. The code, class and
// ledger history stay put: changing them would move posted money between accounts.
func (a *app) updateBankAccount(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in bankAccountInput
	if !decodeJSON(w, r, &in) || !validBankAccount(&in, false) {
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
	var isBank bool
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&isBank); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if !isBank {
		writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
		return
	}
	var openDate any
	if in.OpeningBalanceDate != "" {
		openDate = in.OpeningBalanceDate
	}
	if _, err := a.db.Exec(ctx, `UPDATE accounts SET name=$3, bank_account_number=$4, bank_account_type=$5,
		bank_opening_balance_cents=$6, bank_opening_balance_date=$7, bank_statement_balance_cents=$8,
		bank_currency=$9, archived=$10 WHERE org_id=$1 AND id=$2`,
		access.ID, id, in.Name, in.AccountNumber, in.AccountType, in.OpeningBalanceCents, openDate,
		in.StatementBalanceCents, in.Currency, in.Archived); err != nil {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'bank.account_updated',jsonb_build_object('account_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
