package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Recurring expenses: a profile posts one paid expense per due date when someone runs it. Nothing runs by itself.

type recurringExpenseInput struct {
	OrgID                int64  `json:"org_id"`
	Name                 string `json:"name"`
	RepeatEvery          int    `json:"repeat_every"`
	RepeatUnit           string `json:"repeat_unit"`
	StartDate            string `json:"start_date"`
	EndsOn               string `json:"ends_on"`
	ExpenseAccountID     int64  `json:"expense_account_id"`
	PaidThroughAccountID int64  `json:"paid_through_account_id"`
	CustomerID           int64  `json:"customer_id"`
	Payee                string `json:"payee"`
	AmountCents          int64  `json:"amount_cents"`
	Notes                string `json:"notes"`
}

// nthRun counts from the start date, so a 31st never drifts to the 3rd after a short month.
func nthRun(start time.Time, every int, unit string, k int) time.Time {
	n := every * k
	switch unit {
	case "week":
		return start.AddDate(0, 0, 7*n)
	case "year":
		n *= 12
	}
	t := time.Date(start.Year(), start.Month()+time.Month(n), 1, 0, 0, 0, 0, time.UTC)
	last := t.AddDate(0, 1, -1).Day()
	if start.Day() < last {
		last = start.Day()
	}
	return t.AddDate(0, 0, last-1)
}

func (a *app) createRecurringExpense(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in recurringExpenseInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Payee = strings.TrimSpace(in.Name), strings.TrimSpace(in.Payee)
	start, ok := parseDay(in.StartDate)
	var ends *time.Time
	if ok && in.EndsOn != "" {
		e, good := parseDay(in.EndsOn)
		ok, ends = good && !e.Before(start), &e
	}
	if !ok || in.Name == "" || in.Payee == "" || !validTaxText(in.Name, 200) || !validTaxText(in.Payee, 200) || !validTaxText(in.Notes, 4000) ||
		in.RepeatEvery < 1 || in.RepeatEvery > 12 || !validStage(in.RepeatUnit, "week", "month", "year") ||
		in.ExpenseAccountID <= 0 || in.PaidThroughAccountID <= 0 || in.CustomerID < 0 || in.AmountCents < 1 || in.AmountCents > maxMoneyCents {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	for _, check := range []struct {
		id     int64
		wanted string
	}{{in.ExpenseAccountID, "expense"}, {in.PaidThroughAccountID, "asset"}} {
		var typ string
		err := a.db.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, check.id).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != check.wanted) {
			writeError(w, 422, "invalid_expense_account")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if in.CustomerID > 0 {
		var kind string
		err := a.db.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.CustomerID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
			writeError(w, 422, "invalid_contact")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
	}
	var id int64
	if err := a.db.QueryRow(ctx, `INSERT INTO recurring_expense_profiles (org_id,name,repeat_every,repeat_unit,start_date,ends_on,next_run,expense_account_id,paid_through_account_id,customer_id,payee,amount_cents,notes,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$5,$7,$8,NULLIF($9,0),$10,$11,$12,$13) RETURNING id`,
		access.ID, in.Name, in.RepeatEvery, in.RepeatUnit, start, ends, in.ExpenseAccountID, in.PaidThroughAccountID, in.CustomerID, in.Payee, in.AmountCents, in.Notes, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "next_run": day(start)})
}

func (a *app) listRecurringExpenses(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT p.id,p.name,p.repeat_every,p.repeat_unit,p.start_date::text,COALESCE(p.ends_on::text,''),p.next_run::text,p.expense_account_id,p.paid_through_account_id,COALESCE(p.customer_id,0),p.payee,p.amount_cents,p.notes,p.active,
		(SELECT count(*) FROM expenses e WHERE e.org_id=p.org_id AND e.recurring_profile_id=p.id)
		FROM recurring_expense_profiles p WHERE p.org_id=$1 ORDER BY p.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type profile struct {
		ID                   int64  `json:"id"`
		Name                 string `json:"name"`
		RepeatEvery          int    `json:"repeat_every"`
		RepeatUnit           string `json:"repeat_unit"`
		StartDate            string `json:"start_date"`
		EndsOn               string `json:"ends_on"`
		NextRun              string `json:"next_run"`
		ExpenseAccountID     int64  `json:"expense_account_id"`
		PaidThroughAccountID int64  `json:"paid_through_account_id"`
		CustomerID           int64  `json:"customer_id"`
		Payee                string `json:"payee"`
		AmountCents          int64  `json:"amount_cents"`
		Notes                string `json:"notes"`
		Active               bool   `json:"active"`
		Generated            int64  `json:"generated_count"`
	}
	out := []profile{}
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.ID, &p.Name, &p.RepeatEvery, &p.RepeatUnit, &p.StartDate, &p.EndsOn, &p.NextRun, &p.ExpenseAccountID, &p.PaidThroughAccountID, &p.CustomerID, &p.Payee, &p.AmountCents, &p.Notes, &p.Active, &p.Generated); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// recurringExpenseHistory lists posted expenses for one profile, newest first.
// ponytail: return at most 200 entries; add pagination when longer histories are needed.
func (a *app) recurringExpenseHistory(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var exists bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM recurring_expense_profiles WHERE org_id=$1 AND id=$2)`, access.ID, id).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,recurring_run_date::text,amount_cents FROM expenses WHERE org_id=$1 AND recurring_profile_id=$2 ORDER BY recurring_run_date DESC,id DESC LIMIT 200`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type expense struct {
		ID          int64  `json:"id"`
		Date        string `json:"date"`
		AmountCents int64  `json:"amount_cents"`
	}
	out := []expense{}
	for rows.Next() {
		var e expense
		if err := rows.Scan(&e.ID, &e.Date, &e.AmountCents); err != nil {
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

// stopRecurringExpense turns a profile off or back on. Expenses already generated stay.
func (a *app) stopRecurringExpense(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID  int64 `json:"org_id"`
		ID     int64 `json:"id"`
		Active bool  `json:"active"`
	}
	if !decodeJSON(w, r, &in) || in.ID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE recurring_expense_profiles SET active=$3 WHERE org_id=$1 AND id=$2`, access.ID, in.ID, in.Active)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "active": in.Active})
}

// runRecurringExpenses posts an expense for every due date up to as_of (at most 60 per profile per call).
// The unique (profile, run date) constraint and the org lock make a repeated run post nothing twice.
func (a *app) runRecurringExpenses(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		AsOf  string `json:"as_of"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	asOf, ok := parseDay(in.AsOf)
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
	type due struct {
		id, runs, expense, paid, customer, amount int64
		every                                     int
		unit, name, payee, notes                  string
		start, next                               time.Time
		ends                                      *time.Time
	}
	rows, err := tx.Query(ctx, `SELECT id,runs_done,expense_account_id,paid_through_account_id,COALESCE(customer_id,0),amount_cents,repeat_every,repeat_unit,name,payee,notes,start_date,next_run,ends_on
		FROM recurring_expense_profiles WHERE org_id=$1 AND active AND next_run<=$2 ORDER BY id FOR UPDATE`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.runs, &d.expense, &d.paid, &d.customer, &d.amount, &d.every, &d.unit, &d.name, &d.payee, &d.notes, &d.start, &d.next, &d.ends); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		dues = append(dues, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	created := []int64{}
	for _, d := range dues {
		for n := 0; n < 60 && !d.next.After(asOf) && (d.ends == nil || !d.next.After(*d.ends)); n++ {
			expense := expenseInput{PaidThroughAccountID: d.paid, CustomerID: d.customer, Payee: d.payee, Description: d.name, ExpenseAccountID: d.expense, AmountCents: d.amount, Notes: d.notes}
			key := fmt.Sprintf("recexp:%d:%s", d.id, day(d.next))
			hash := sha256.Sum256([]byte(key))
			id, _, err := insertExpense(ctx, tx, access.ID, u.ID, d.next, expense, []expenseItem{{Description: d.name, AccountID: d.expense, AmountCents: d.amount}}, key, hash[:], []byte("{}"), d.id, d.next)
			if err != nil {
				if isPeriodClosed(err) {
					writeError(w, 422, "period_closed")
				} else {
					serverError(w, err)
				}
				return
			}
			created = append(created, id)
			d.runs++
			d.next = nthRun(d.start, d.every, d.unit, int(d.runs))
		}
		active := d.ends == nil || !d.next.After(*d.ends)
		if _, err := tx.Exec(ctx, `UPDATE recurring_expense_profiles SET runs_done=$3,next_run=$4,active=$5 WHERE org_id=$1 AND id=$2`, access.ID, d.id, d.runs, d.next, active); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'recurring_expense.run',jsonb_build_object('created',$3::int))`, access.ID, u.ID, len(created)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created_expense_ids": created})
}
