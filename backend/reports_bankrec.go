package main

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// bankReconciliation: GET /api/v1/reports/bank-reconciliation?org_id&from&to&bank_account_id
// One bank account at a time (R16). Statement closing balance is the sum of that account's imported lines;
// the ledger balance is the account's own journal total. After every line is matched they agree.
func (a *app) bankReconciliation(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	bankID, ok := parseAccountID(q.Get("bank_account_id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if bankID == 0 {
		if bankID, ok = a.defaultBankAccount(w, ctx, access.ID); !ok {
			return
		}
	}
	var code string
	var isBank bool
	if err := a.db.QueryRow(ctx, `SELECT code, COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, bankID).Scan(&code, &isBank); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
			return
		}
		serverError(w, err)
		return
	}
	if !isBank {
		writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
		return
	}
	type bucket struct {
		Lines    int64 `json:"lines"`
		InCents  int64 `json:"in_cents"`
		OutCents int64 `json:"out_cents"`
	}
	var matched, unmatched bucket
	rows, err := a.db.Query(ctx, `SELECT l.matched_payment_kind IS NOT NULL, count(*),
		COALESCE(SUM(CASE WHEN l.amount_cents>0 THEN l.amount_cents END),0),
		COALESCE(SUM(CASE WHEN l.amount_cents<0 THEN -l.amount_cents END),0)
		FROM bank_statement_lines l WHERE l.org_id=$1 AND l.bank_account_id=$4 AND l.line_date BETWEEN $2::date AND $3::date
		GROUP BY 1`, access.ID, from, to, bankID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var isMatched bool
		var v bucket
		if err := rows.Scan(&isMatched, &v.Lines, &v.InCents, &v.OutCents); err != nil {
			serverError(w, err)
			return
		}
		if isMatched {
			matched = v
		} else {
			unmatched = v
		}
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	var statementClosing int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM bank_statement_lines WHERE org_id=$1 AND bank_account_id=$4 AND line_date<=$3::date AND line_date>=$2::date`,
		access.ID, from, to, bankID).Scan(&statementClosing); err != nil {
		serverError(w, err)
		return
	}
	var ledger int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0) FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		WHERE jl.org_id=$1 AND jl.account_id=$2 AND j.entry_date<=$3::date`, access.ID, bankID, to).Scan(&ledger); err != nil {
		serverError(w, err)
		return
	}
	var matchCount int64
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM bank_line_matches m JOIN bank_statement_lines l ON l.org_id=m.org_id AND l.id=m.line_id
		WHERE m.org_id=$1 AND l.bank_account_id=$2 AND l.line_date BETWEEN $3::date AND $4::date`, access.ID, bankID, from, to).Scan(&matchCount); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matched": matched, "unmatched": unmatched,
		"ledger_balance_cents":    ledger,
		"statement_closing_cents": statementClosing,
		"difference_cents":        statementClosing - ledger,
		"matched_documents":       matchCount,
		"account_code":            code,
		"bank_account_id":         bankID,
	})
}
