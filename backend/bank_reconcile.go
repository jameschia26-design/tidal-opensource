package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Reconciliation measures a bank account over a period: what the statement says it started and ended
// at, what the ledger says, and the gap between them. A reconciliation is only saved when every line
// in the period is matched and the gap is zero.

type bankFigures struct {
	OpeningCents    int64 `json:"opening_cents"`
	StatementCents  int64 `json:"statement_cents"`
	LedgerCents     int64 `json:"ledger_cents"`
	DifferenceCents int64 `json:"difference_cents"`
	UnmatchedLines  int64 `json:"unmatched_lines"`
	LinesTotal      int64 `json:"lines_total"`
}

func (a *app) bankFigures(ctx context.Context, q rowQuerier, orgID, bankID int64, from, to string) (bankFigures, error) {
	var out bankFigures
	var accountOpening int64
	if err := q.QueryRow(ctx, `SELECT bank_opening_balance_cents FROM accounts
		WHERE org_id=$1 AND id=$2 AND is_bank_account`, orgID, bankID).Scan(&accountOpening); err != nil {
		return out, err
	}
	var beforePeriod int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM bank_statement_lines
		WHERE org_id=$1 AND bank_account_id=$2 AND line_date < $3::date`, orgID, bankID, from).Scan(&beforePeriod); err != nil {
		return out, err
	}
	var inPeriod int64
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0), count(*),
		count(*) FILTER (WHERE matched_payment_kind IS NULL)
		FROM bank_statement_lines WHERE org_id=$1 AND bank_account_id=$2 AND line_date BETWEEN $3::date AND $4::date`,
		orgID, bankID, from, to).Scan(&inPeriod, &out.LinesTotal, &out.UnmatchedLines); err != nil {
		return out, err
	}
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0) FROM journal_lines jl
		JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		WHERE jl.org_id=$1 AND jl.account_id=$2 AND j.entry_date <= $3::date`, orgID, bankID, to).Scan(&out.LedgerCents); err != nil {
		return out, err
	}
	out.OpeningCents = accountOpening + beforePeriod
	out.StatementCents = out.OpeningCents + inPeriod
	out.DifferenceCents = out.StatementCents - out.LedgerCents
	return out, nil
}

// getBankReconciliation: GET /api/v1/bank/reconcile — the running figures for the reconcile screen.
func (a *app) getBankReconciliation(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	if !ok || bankID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	from, to := q.Get("from"), q.Get("to")
	if _, ok := parseDay(from); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, ok := parseDay(to); !ok || to < from {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	fig, err := a.bankFigures(r.Context(), a.db, access.ID, bankID, from, to)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bank_account_id": bankID, "from": from, "to": to,
		"opening_cents": fig.OpeningCents, "statement_cents": fig.StatementCents,
		"ledger_cents": fig.LedgerCents, "difference_cents": fig.DifferenceCents,
		"unmatched_lines": fig.UnmatchedLines, "lines_total": fig.LinesTotal,
	})
}

// saveBankReconciliation: POST /api/v1/bank/reconciliations — records the period and balances once the
// statement and the ledger agree. A gap or an unmatched line is refused, never stored.
func (a *app) saveBankReconciliation(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID  int64  `json:"org_id"`
		BankID int64  `json:"bank_account_id"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.From, in.To = strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	if in.BankID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, ok := parseDay(in.From); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, ok := parseDay(in.To); !ok || in.To < in.From {
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
	fig, err := a.bankFigures(ctx, tx, access.ID, in.BankID, in.From, in.To)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_bank_account")
			return
		}
		serverError(w, err)
		return
	}
	if fig.UnmatchedLines > 0 {
		writeError(w, http.StatusUnprocessableEntity, "unmatched_lines")
		return
	}
	if fig.DifferenceCents != 0 {
		writeError(w, http.StatusUnprocessableEntity, "difference_not_zero")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO bank_reconciliations (org_id, bank_account_id, period_from, period_to,
		opening_balance_cents, statement_closing_cents, ledger_balance_cents, difference_cents, lines_total, created_by)
		VALUES ($1,$2,$3::date,$4::date,$5,$6,$7,$8,$9,$10) RETURNING id`,
		access.ID, in.BankID, in.From, in.To, fig.OpeningCents, fig.StatementCents, fig.LedgerCents,
		fig.DifferenceCents, fig.LinesTotal, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET bank_statement_balance_cents=$3 WHERE org_id=$1 AND id=$2`,
		access.ID, in.BankID, fig.StatementCents); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail)
		VALUES($1,$2,'bank.reconciled',jsonb_build_object('reconciliation_id',$3::bigint,'bank_account_id',$4::bigint,'period_to',$5::text))`,
		access.ID, u.ID, id, in.BankID, in.To); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "statement_cents": fig.StatementCents,
		"ledger_cents": fig.LedgerCents, "difference_cents": fig.DifferenceCents, "lines_total": fig.LinesTotal})
}

// listBankReconciliations: GET /api/v1/bank/reconciliations — saved reconciliations, newest first.
func (a *app) listBankReconciliations(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	bankID, ok := parseAccountID(r.URL.Query().Get("bank_account_id"))
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT rc.id, rc.bank_account_id, a.name, rc.period_from::text, rc.period_to::text,
		rc.opening_balance_cents, rc.statement_closing_cents, rc.ledger_balance_cents, rc.difference_cents,
		rc.lines_total, rc.created_at::text, COALESCE(u.email,'')
		FROM bank_reconciliations rc
		JOIN accounts a ON a.org_id=rc.org_id AND a.id=rc.bank_account_id
		LEFT JOIN users u ON u.id=rc.created_by
		WHERE rc.org_id=$1 AND ($2=0 OR rc.bank_account_id=$2)
		ORDER BY rc.period_to DESC, rc.id DESC LIMIT 200`, access.ID, bankID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type reconciliation struct {
		ID              int64  `json:"id"`
		BankID          int64  `json:"bank_account_id"`
		AccountName     string `json:"account_name"`
		From            string `json:"from"`
		To              string `json:"to"`
		OpeningCents    int64  `json:"opening_cents"`
		StatementCents  int64  `json:"statement_cents"`
		LedgerCents     int64  `json:"ledger_cents"`
		DifferenceCents int64  `json:"difference_cents"`
		LinesTotal      int64  `json:"lines_total"`
		CreatedAt       string `json:"created_at"`
		CreatedBy       string `json:"created_by"`
	}
	out := []reconciliation{}
	for rows.Next() {
		var x reconciliation
		if err := rows.Scan(&x.ID, &x.BankID, &x.AccountName, &x.From, &x.To, &x.OpeningCents,
			&x.StatementCents, &x.LedgerCents, &x.DifferenceCents, &x.LinesTotal, &x.CreatedAt, &x.CreatedBy); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reconciliations": out})
}
