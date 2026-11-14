package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Multicurrency (Phase 3b). Books stay in SGD. A document keeps the currency and rate it was struck
// at; the currency table gives a rate by date, month-end revaluation moves open foreign balances to
// the period rate, and a settlement at a different rate posts the realised difference.
//
// FX accounts are created on demand: realised gains/losses and unrealised gains/losses, kept apart so
// the P&L reads "Other income" and "Other expense" the way Xero's chart does.

const (
	acctRealisedGain   = "499"
	acctRealisedLoss   = "599"
	acctUnrealisedGain = "899"
	acctUnrealisedLoss = "898"

	fxRateScale = 1000000
)

var fxAccounts = []struct{ code, name, typ, class string }{
	{acctRealisedGain, "Realised Currency Gains", "revenue", "other_income"},
	{acctRealisedLoss, "Realised Currency Losses", "expense", "other_expense"},
	{acctUnrealisedGain, "Unrealised Currency Gains", "revenue", "other_income"},
	{acctUnrealisedLoss, "Unrealised Currency Losses", "expense", "other_expense"},
}

// ensureFXAccounts creates the four FX accounts when missing and returns code -> id.
func ensureFXAccounts(ctx context.Context, tx pgx.Tx, orgID int64) (map[string]int64, error) {
	ids := make(map[string]int64, len(fxAccounts))
	for _, a := range fxAccounts {
		var id int64
		var typ, class string
		err := tx.QueryRow(ctx,
			`INSERT INTO accounts (org_id, code, name, type, class) VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (org_id, code) DO UPDATE SET name = accounts.name
			 RETURNING id, type, class`, orgID, a.code, a.name, a.typ, a.class).Scan(&id, &typ, &class)
		if err != nil {
			return nil, err
		}
		if typ != a.typ || class != a.class {
			return nil, fmt.Errorf("account %s is %s/%s, want %s/%s", a.code, typ, class, a.typ, a.class)
		}
		ids[a.code] = id
	}
	return ids, nil
}

// validFXRate bounds a typed or stored exchange rate to the same range documents already accept.
func validFXRate(rateMicro int64) bool { return rateMicro >= 1000 && rateMicro <= 1000000000000 }

// fxMulDivRound computes a*b/c, rounded half up, signed. Foreign amounts and their SGD value can
// exceed int64 on the way through, so the multiplication uses big integers.
func fxMulDivRound(a, b, c int64) int64 {
	if c == 0 {
		return 0
	}
	neg := (a < 0) != (b < 0) != (c < 0)
	abs := func(v int64) int64 {
		if v < 0 {
			return -v
		}
		return v
	}
	x := new(big.Int).Mul(big.NewInt(abs(a)), big.NewInt(abs(b)))
	x.Add(x, big.NewInt(abs(c)/2))
	x.Quo(x, big.NewInt(abs(c)))
	n := x.Int64()
	if neg {
		return -n
	}
	return n
}

// lookupCurrencyRate returns the latest rate on or before a date. SGD is always 1.
func lookupCurrencyRate(ctx context.Context, q rowQueryer, orgID int64, code, on string) (int64, bool, error) {
	if code == "SGD" {
		return fxRateScale, true, nil
	}
	var rate int64
	err := q.QueryRow(ctx, `SELECT rate_micro FROM currency_rates
		WHERE org_id=$1 AND currency_code=$2 AND rate_date<=$3::date
		ORDER BY rate_date DESC LIMIT 1`, orgID, code, on).Scan(&rate)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return rate, err == nil, err
}

// resolveDocumentCurrency normalises a currency/rate pair and, when a foreign document leaves the rate
// blank, looks it up by the document date. A currency with no rate on or before that date is refused.
func resolveDocumentCurrency(ctx context.Context, q rowQueryer, orgID int64, currency string, rate int64, on string) (string, int64, bool) {
	code, ok := normaliseCurrency(currency)
	if !ok {
		return "", 0, false
	}
	if rate == 0 && code != "SGD" {
		found, have, err := lookupCurrencyRate(ctx, q, orgID, code, on)
		if err != nil || !have {
			return "", 0, false
		}
		rate = found
	}
	return documentCurrency(code, rate)
}

// fxLeg is one FX journal line. cents is signed: positive is a debit, negative a credit.
type fxLeg struct {
	account  int64
	cents    int64
	currency string
}

// postFXJournal writes a balanced journal whose lines keep their deal currency, which is what the
// Foreign Currency Gains and Losses report groups by.
func postFXJournal(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, memo, key string, hash [32]byte, legs []fxLeg) (int64, error) {
	var sum int64
	for _, l := range legs {
		sum += l.cents
	}
	if sum != 0 {
		return 0, fmt.Errorf("unbalanced FX journal %s: %d", key, sum)
	}
	var jid int64
	if err := tx.QueryRow(ctx, `INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, orgID, date, memo, key, hash[:], userID).Scan(&jid); err != nil {
		return 0, err
	}
	line := 1
	for _, l := range legs {
		if l.cents == 0 {
			continue
		}
		debit, credit := l.cents, int64(0)
		if debit < 0 {
			debit, credit = 0, -debit
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, currency)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`, orgID, jid, line, l.account, debit, credit, l.currency); err != nil {
			return 0, err
		}
		line++
	}
	return jid, nil
}

// reverseFXJournal mirrors a journal that a rerun replaces. The unique reversal_of keeps it to once.
func reverseFXJournal(ctx context.Context, tx pgx.Tx, orgID, userID, journalID int64, date time.Time) (int64, error) {
	var existing int64
	err := tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND reversal_of=$2`, orgID, journalID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	key := fmt.Sprintf("fx-reval-rev:%d", journalID)
	hash := sha256.Sum256([]byte(key))
	var jid int64
	if err := tx.QueryRow(ctx, `INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, reversal_of, created_by)
		VALUES ($1,$2,'FX revaluation reversed',$3,$4,$5,$6) RETURNING id`,
		orgID, date, key, hash[:], journalID, userID).Scan(&jid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, currency, tax_direction, description)
		SELECT org_id, $3, line_no, account_id, credit_cents, debit_cents, currency, tax_direction, 'FX revaluation reversed'
		FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, orgID, journalID, jid); err != nil {
		return 0, err
	}
	return jid, nil
}

// ---- currency table ----

type currencyInput struct {
	OrgID int64  `json:"org_id"`
	Code  string `json:"code"`
	Name  string `json:"name"`
}

// listCurrencies: GET /api/v1/currencies?org_id
func (a *app) listCurrencies(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := a.db.Exec(ctx, `INSERT INTO currencies (org_id, code, name, is_base) VALUES ($1,'SGD','Singapore dollar',true)
		ON CONFLICT (org_id, code) DO NOTHING`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(ctx, `SELECT code, name, is_base, active FROM currencies WHERE org_id=$1 ORDER BY is_base DESC, code`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	type rate struct {
		RateDate  string `json:"rate_date"`
		RateMicro int64  `json:"rate_micro"`
	}
	type currency struct {
		Code            string `json:"code"`
		Name            string `json:"name"`
		IsBase          bool   `json:"is_base"`
		Active          bool   `json:"active"`
		LatestRateMicro int64  `json:"latest_rate_micro"`
		LatestRateDate  string `json:"latest_rate_date"`
		Rates           []rate `json:"rates"`
	}
	out := []currency{}
	index := map[string]int{}
	for rows.Next() {
		var c currency
		if err := rows.Scan(&c.Code, &c.Name, &c.IsBase, &c.Active); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		c.Rates = []rate{}
		index[c.Code] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	rateRows, err := a.db.Query(ctx, `SELECT currency_code, rate_date::text, rate_micro
		FROM currency_rates WHERE org_id=$1 ORDER BY currency_code, rate_date DESC`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rateRows.Close()
	for rateRows.Next() {
		var code, on string
		var micro int64
		if err := rateRows.Scan(&code, &on, &micro); err != nil {
			serverError(w, err)
			return
		}
		i, ok := index[code]
		if !ok {
			continue
		}
		out[i].Rates = append(out[i].Rates, rate{RateDate: on, RateMicro: micro})
		if out[i].LatestRateDate == "" {
			out[i].LatestRateMicro, out[i].LatestRateDate = micro, on
		}
	}
	if err := rateRows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"base": "SGD", "currencies": out})
}

// saveCurrency: POST /api/v1/currencies adds a currency; PUT /api/v1/currencies/{code} renames it.
func (a *app) saveCurrency(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in currencyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	code, ok := normaliseCurrency(in.Code)
	if !ok || in.Name == "" || len(in.Name) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if r.Method == http.MethodPut {
		in.Code = strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
		if in.Code != code {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	if code == "SGD" && r.Method == http.MethodPost {
		writeError(w, http.StatusConflict, "currency_exists")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	if r.Method == http.MethodPut {
		tag, err := a.db.Exec(ctx, `UPDATE currencies SET name=$3 WHERE org_id=$1 AND code=$2 AND NOT is_base`, access.ID, code, in.Name)
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": code, "name": in.Name})
		return
	}
	_, err := a.db.Exec(ctx, `INSERT INTO currencies (org_id, code, name) VALUES ($1,$2,$3)`, access.ID, code, in.Name)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "currency_exists")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "name": in.Name, "is_base": false})
}

type currencyRateInput struct {
	OrgID     int64  `json:"org_id"`
	Code      string `json:"code"`
	RateDate  string `json:"rate_date"`
	RateMicro int64  `json:"rate_micro"`
}

// saveCurrencyRate: POST /api/v1/currencies/rates upserts one dated rate.
func (a *app) saveCurrencyRate(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in currencyRateInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	code, ok := normaliseCurrency(in.Code)
	if !ok || code == "SGD" || !validFXRate(in.RateMicro) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, ok := parseDay(in.RateDate); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var exists bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM currencies WHERE org_id=$1 AND code=$2)`, access.ID, code).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO currency_rates (org_id, currency_code, rate_date, rate_micro)
		VALUES ($1,$2,$3::date,$4)
		ON CONFLICT (org_id, currency_code, rate_date)
		DO UPDATE SET rate_micro=EXCLUDED.rate_micro, updated_at=now()`, access.ID, code, in.RateDate, in.RateMicro); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "rate_date": in.RateDate, "rate_micro": in.RateMicro})
}

// ---- revaluation ----

type revalueInput struct {
	OrgID     int64  `json:"org_id"`
	PeriodEnd string `json:"period_end"`
}

type openFXDoc struct {
	currency string
	rate     int64
	open     int64
}

// openFXDocuments lists posted foreign invoices (kind "invoice") or bills (kind "bill") that still owe
// money at period end. Open value is carried in SGD at each document's own rate.
func openFXDocuments(ctx context.Context, tx pgx.Tx, orgID int64, kind, on string) ([]openFXDoc, error) {
	var sql string
	if kind == "invoice" {
		sql = `SELECT i.currency, i.fx_rate_micro,
			i.total_cents
			- COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id),0)
			- COALESCE((SELECT SUM(c.amount_cents) FROM credit_notes c WHERE c.org_id=i.org_id AND c.invoice_id=i.id),0)
			- COALESCE((SELECT SUM(a.amount_cents) FROM customer_credit_applications a WHERE a.org_id=i.org_id AND a.invoice_id=i.id),0)
			FROM invoices i
			WHERE i.org_id=$1 AND i.status='posted' AND i.currency<>'SGD' AND i.invoice_date<=$2::date
			  AND NOT EXISTS (SELECT 1 FROM document_reversals d WHERE d.org_id=i.org_id AND d.doc_kind='invoice' AND d.doc_id=i.id)
			FOR UPDATE OF i`
	} else {
		sql = `SELECT b.currency, b.fx_rate_micro,
			b.total_cents
			- COALESCE((SELECT SUM(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id
				AND NOT EXISTS (SELECT 1 FROM document_reversals d WHERE d.org_id=p.org_id AND d.doc_kind='bill_payment' AND d.doc_id=p.id)),0)
			- COALESCE((SELECT SUM(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id),0)
			FROM bills b
			WHERE b.org_id=$1 AND b.status='posted' AND b.currency<>'SGD' AND b.bill_date<=$2::date
			  AND NOT EXISTS (SELECT 1 FROM document_reversals d WHERE d.org_id=b.org_id AND d.doc_kind='bill' AND d.doc_id=b.id)
			FOR UPDATE OF b`
	}
	rows, err := tx.Query(ctx, sql, orgID, on)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openFXDoc
	for rows.Next() {
		var d openFXDoc
		if err := rows.Scan(&d.currency, &d.rate, &d.open); err != nil {
			return nil, err
		}
		if d.open > 0 {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// revalueFX: POST /api/v1/fx/revalue — moves every open foreign balance to the period-end rate and
// posts the difference as unrealised gain or loss. Rerunning the same period reverses the earlier
// journal first, so no double posting survives.
func (a *app) revalueFX(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in revalueInput
	if !decodeJSON(w, r, &in) {
		return
	}
	on, ok := parseDay(in.PeriodEnd)
	if !ok {
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
	accounts, err := fxAccountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var runID, previousJournal, previousReversal int64
	var found bool
	err = tx.QueryRow(ctx, `SELECT id, journal_id, COALESCE(reversal_journal_id,0) FROM fx_revaluation_runs
		WHERE org_id=$1 AND period_end=$2::date FOR UPDATE`, access.ID, in.PeriodEnd).Scan(&runID, &previousJournal, &previousReversal)
	if err == nil {
		found = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	var reversalID int64
	if found {
		reversalID, err = reverseFXJournal(ctx, tx, access.ID, u.ID, previousJournal, on)
		if err != nil {
			serverError(w, err)
			return
		}
	}

	invoices, err := openFXDocuments(ctx, tx, access.ID, "invoice", in.PeriodEnd)
	if err != nil {
		serverError(w, err)
		return
	}
	bills, err := openFXDocuments(ctx, tx, access.ID, "bill", in.PeriodEnd)
	if err != nil {
		serverError(w, err)
		return
	}
	type movement struct {
		currency   string
		rate       int64
		receivable int64 // net change to the SGD carrying value of receivables
		payable    int64 // net change to the SGD carrying value of payables
		gain, loss int64
	}
	movements := map[string]*movement{}
	order := []string{}
	apply := func(docs []openFXDoc, receivable bool) error {
		for _, d := range docs {
			periodRate, have, err := lookupCurrencyRate(ctx, tx, access.ID, d.currency, in.PeriodEnd)
			if err != nil {
				return err
			}
			if !have {
				return errMissingRate
			}
			foreign := fxMulDivRound(d.open, fxRateScale, d.rate)
			revalued := fxMulDivRound(foreign, periodRate, fxRateScale)
			diff := revalued - d.open
			m, ok := movements[d.currency]
			if !ok {
				m = &movement{currency: d.currency, rate: periodRate}
				movements[d.currency] = m
				order = append(order, d.currency)
			}
			if receivable {
				m.receivable += diff
			} else {
				m.payable += diff
			}
		}
		return nil
	}
	if err := apply(invoices, true); err != nil {
		if errors.Is(err, errMissingRate) {
			writeError(w, http.StatusUnprocessableEntity, "missing_exchange_rate")
			return
		}
		serverError(w, err)
		return
	}
	if err := apply(bills, false); err != nil {
		if errors.Is(err, errMissingRate) {
			writeError(w, http.StatusUnprocessableEntity, "missing_exchange_rate")
			return
		}
		serverError(w, err)
		return
	}
	var legs []fxLeg
	var total int64
	rows := []map[string]any{}
	for _, code := range order {
		m := movements[code]
		// A rise in an asset or fall in a liability is a gain; the reverse is a loss.
		gain := m.receivable - m.payable
		legs = append(legs, fxLeg{accounts[acctAR], m.receivable, code})
		legs = append(legs, fxLeg{accounts[acctAP], -m.payable, code})
		if gain > 0 {
			legs = append(legs, fxLeg{accounts[acctUnrealisedGain], -gain, code})
			m.gain = gain
		} else if gain < 0 {
			legs = append(legs, fxLeg{accounts[acctUnrealisedLoss], -gain, code})
			m.loss = -gain
		}
		total += gain
		rows = append(rows, map[string]any{
			"currency": code, "rate_micro": m.rate,
			"receivable_cents": m.receivable, "payable_cents": m.payable,
			"gain_cents": m.gain, "loss_cents": m.loss,
		})
	}
	var journalID int64
	if len(legs) > 0 {
		key := fmt.Sprintf("fx-reval:%s:%d", in.PeriodEnd, time.Now().UnixNano())
		hash := sha256.Sum256([]byte(key))
		journalID, err = postFXJournal(ctx, tx, access.ID, u.ID, on, "FX revaluation at "+in.PeriodEnd, key, hash, legs)
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if found {
		if _, err := tx.Exec(ctx, `UPDATE fx_revaluation_runs SET journal_id=$3, reversal_journal_id=NULLIF($4,0), total_cents=$5, updated_at=now()
			WHERE org_id=$1 AND id=$2`, access.ID, runID, journalID, reversalID, total); err != nil {
			serverError(w, err)
			return
		}
	} else if journalID != 0 {
		if err := tx.QueryRow(ctx, `INSERT INTO fx_revaluation_runs (org_id, period_end, journal_id, total_cents, created_by)
			VALUES ($1,$2::date,$3,$4,$5) RETURNING id`, access.ID, in.PeriodEnd, journalID, total, u.ID).Scan(&runID); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period_end": in.PeriodEnd, "journal_id": journalID, "reversal_journal_id": reversalID,
		"total_cents": total, "currencies": rows,
	})
}

var errMissingRate = errors.New("missing exchange rate")

// ---- FX report ----

type fxMovement struct {
	Code  string `json:"currency"`
	Kind  string `json:"kind"`
	Cents int64  `json:"amount_cents"`
}

// fxGainsLosses: GET /api/v1/reports/fx-gains-losses?org_id&from&to — movements on the FX accounts by
// currency and kind, reconciled to the FX account balance movement for the period.
func (a *app) fxGainsLosses(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
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
	fxCodes := []string{acctRealisedGain, acctRealisedLoss, acctUnrealisedGain, acctUnrealisedLoss}
	query := func(condition string, args ...any) (int64, error) {
		var balance int64
		err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(jl.credit_cents-jl.debit_cents),0)
			FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
			JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
			WHERE jl.org_id=$1 AND a.code = ANY($2) AND `+condition, append([]any{access.ID, fxCodes}, args...)...).Scan(&balance)
		return balance, err
	}
	rows, err := a.db.Query(ctx, `SELECT COALESCE(jl.currency,'SGD'),
		CASE WHEN a.code IN ('499','599') THEN 'realised' ELSE 'unrealised' END,
		COALESCE(SUM(jl.credit_cents-jl.debit_cents),0)
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND a.code = ANY($2) AND j.entry_date BETWEEN $3::date AND $4::date
		GROUP BY 1,2 ORDER BY 1,2`, access.ID, fxCodes, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []fxMovement{}
	var realised, unrealised int64
	for rows.Next() {
		var m fxMovement
		if err := rows.Scan(&m.Code, &m.Kind, &m.Cents); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		if m.Kind == "realised" {
			realised += m.Cents
		} else {
			unrealised += m.Cents
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	opening, err := query(`j.entry_date < $3::date`, from)
	if err != nil {
		serverError(w, err)
		return
	}
	closing, err := query(`j.entry_date <= $3::date`, to)
	if err != nil {
		serverError(w, err)
		return
	}
	movement := closing - opening
	// The FX accounts exist only after the first run; a missing account is a zero movement, not an error.
	writeJSON(w, http.StatusOK, map[string]any{
		"rows": out, "realised_cents": realised, "unrealised_cents": unrealised,
		"total_cents": realised + unrealised, "opening_balance_cents": opening,
		"closing_balance_cents": closing, "movement_cents": movement,
		"reconciles": movement == realised+unrealised,
	})
}

// fxAccountIDs returns the standard accounts plus the four FX accounts.
func fxAccountIDs(ctx context.Context, tx pgx.Tx, orgID int64) (map[string]int64, error) {
	ids, err := accountIDs(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	fx, err := ensureFXAccounts(ctx, tx, orgID)
	if err != nil {
		return nil, err
	}
	for code, id := range fx {
		ids[code] = id
	}
	return ids, nil
}

// realisedFXLeg books a settlement difference: a negative diff is a gain, positive is a loss.
func realisedFXLeg(accounts map[string]int64, diff int64, currency string) fxLeg {
	if diff > 0 {
		return fxLeg{accounts[acctRealisedLoss], diff, currency}
	}
	return fxLeg{accounts[acctRealisedGain], diff, currency}
}
