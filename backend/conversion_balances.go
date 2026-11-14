package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// Conversion balances: one opening journal as of a conversion date. Any difference between total debits and
// credits is booked to Opening Balance Equity (3900), so the books always balance.
// Posted once per company; a different second submission is refused (use a manual journal to correct).
// ponytail: no edit after posting; add when asked.

// conversionInput carries one set of opening balances per date. The comparative set is optional and
// is Xero's "Add Comparative Balances": a second, earlier opening journal, usually last year's closing
// position, so reports that compare years have something to compare against.
type conversionInput struct {
	OrgID            int64         `json:"org_id"`
	Date             string        `json:"conversion_date"`
	Lines            []journalLine `json:"lines"`
	ComparativeDate  string        `json:"comparative_date"`
	ComparativeLines []journalLine `json:"comparative_lines"`
}

const (
	conversionKey         = "conversion-balances"
	conversionCompareKey  = "conversion-comparative-balances"
	conversionMaxLines    = 200
	conversionMemo        = "Conversion balances"
	conversionCompareMemo = "Comparative conversion balances"
)

// validConversionLines checks one balance grid and returns its debit and credit totals.
func validConversionLines(lines []journalLine) (debit, credit int64, ok bool) {
	if len(lines) < 1 || len(lines) > conversionMaxLines {
		return 0, 0, false
	}
	for _, l := range lines {
		if !validTaxText(l.Description, 500) || l.AccountID <= 0 || l.DebitCents < 0 || l.CreditCents < 0 || (l.DebitCents == 0) == (l.CreditCents == 0) || l.DebitCents > maxMoneyCents || l.CreditCents > maxMoneyCents {
			return 0, 0, false
		}
		debit += l.DebitCents
		credit += l.CreditCents
		if debit > maxMoneyCents*200 || credit > maxMoneyCents*200 {
			return 0, 0, false
		}
	}
	return debit, credit, true
}

func (a *app) postConversionBalances(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in conversionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	day, ok := parseDay(in.Date)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	debit, credit, ok := validConversionLines(in.Lines)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	compare := in.ComparativeDate != "" || len(in.ComparativeLines) > 0
	var compareDay time.Time
	if compare {
		var compareOK bool
		compareDay, compareOK = parseDay(in.ComparativeDate)
		if !compareOK || !compareDay.Before(day) {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		if _, _, ok := validConversionLines(in.ComparativeLines); !ok {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	in.OrgID = access.ID
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
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	// Receivable and payable balances come from each customer/supplier's opening balance, and the
	// difference goes to Opening Balance Equity; entering them here would double-count.
	for _, grid := range [][]journalLine{in.Lines, in.ComparativeLines} {
		for _, l := range grid {
			if l.AccountID == ids[acctAR] || l.AccountID == ids[acctAP] || l.AccountID == ids[acctOBE] {
				writeError(w, http.StatusBadRequest, "reserved_account")
				return
			}
		}
	}
	// post writes one opening journal, keeping the balancing difference in Opening Balance Equity.
	post := func(key, memo string, when time.Time, lines []journalLine, d, c int64) (int64, bool, bool) {
		enc, _ := json.Marshal(struct {
			Key   string        `json:"key"`
			Date  string        `json:"date"`
			Lines []journalLine `json:"lines"`
		}{key, when.Format("2006-01-02"), lines})
		hash := sha256.Sum256(enc)
		var existing int64
		var existingHash []byte
		switch err := tx.QueryRow(ctx, `SELECT id,request_hash FROM journals WHERE org_id=$1 AND idempotency_key=$2`, access.ID, key).Scan(&existing, &existingHash); {
		case err == nil:
			if string(existingHash) != string(hash[:]) {
				writeError(w, http.StatusConflict, "conversion_balances_posted")
				return 0, false, false
			}
			return existing, true, true
		case !errors.Is(err, pgx.ErrNoRows):
			serverError(w, err)
			return 0, false, false
		}
		full := append([]journalLine(nil), lines...)
		if d != c {
			l := journalLine{Description: "Balancing difference", AccountID: ids[acctOBE]}
			if d > c {
				l.CreditCents = d - c
			} else {
				l.DebitCents = c - d
			}
			full = append(full, l)
		}
		for _, l := range full {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2)`, access.ID, l.AccountID).Scan(&exists); err != nil {
				serverError(w, err)
				return 0, false, false
			}
			if !exists {
				writeError(w, http.StatusBadRequest, "invalid_account")
				return 0, false, false
			}
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, access.ID, when, memo, key, hash[:], u.ID).Scan(&id)
		if isPeriodClosed(err) {
			writeError(w, http.StatusUnprocessableEntity, "period_closed")
			return 0, false, false
		}
		if err != nil {
			serverError(w, err)
			return 0, false, false
		}
		for i, l := range full {
			if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description) VALUES($1,$2,$3,$4,$5,$6,$7)`, access.ID, id, i+1, l.AccountID, l.DebitCents, l.CreditCents, l.Description); err != nil {
				serverError(w, err)
				return 0, false, false
			}
		}
		return id, false, true
	}
	id, replayed, ok := post(conversionKey, conversionMemo, day, in.Lines, debit, credit)
	if !ok {
		return
	}
	var compareID int64
	if compare {
		cd, cc, _ := validConversionLines(in.ComparativeLines)
		compareID, _, ok = post(conversionCompareKey, conversionCompareMemo, compareDay, in.ComparativeLines, cd, cc)
		if !ok {
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'conversion.posted',jsonb_build_object('journal_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"id": id}
	if compareID != 0 {
		out["comparative_id"] = compareID
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// getConversionBalances returns the posted conversion journal id and date, or null.
func (a *app) getConversionBalances(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	var id int64
	var date string
	err := a.db.QueryRow(r.Context(), `SELECT id,entry_date::text FROM journals WHERE org_id=$1 AND idempotency_key=$2`, access.ID, conversionKey).Scan(&id, &date)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"journal": nil})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"journal": map[string]any{"id": id, "conversion_date": date}})
}
