package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"time"
)

// Repeating journals: a profile posts one balanced manual journal per due date when someone runs it.
// Nothing runs by itself. Tax rate per line is not offered (GST is limited to invoices).

type repeatingJournalInput struct {
	OrgID       int64         `json:"org_id"`
	Narration   string        `json:"narration"`
	RepeatEvery int           `json:"repeat_every"`
	RepeatUnit  string        `json:"repeat_unit"`
	StartDate   string        `json:"start_date"`
	EndsOn      string        `json:"ends_on"`
	Lines       []journalLine `json:"lines"`
}

func (a *app) createRepeatingJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in repeatingJournalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	start, ok := parseDay(in.StartDate)
	var ends *time.Time
	if ok && in.EndsOn != "" {
		e, good := parseDay(in.EndsOn)
		ok, ends = good && !e.Before(start), &e
	}
	// Reuse manual-journal validation: balanced, positive, bounded lines and a narration.
	check := journalInput{EntryDate: in.StartDate, Memo: in.Narration, IdempotencyKey: "x", Lines: in.Lines}
	if !ok || !validJournal(&check) || check.Memo == "" || in.RepeatEvery < 1 || in.RepeatEvery > 12 || !validStage(in.RepeatUnit, "week", "month", "year") {
		writeError(w, http.StatusBadRequest, "invalid_journal")
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
	for _, l := range in.Lines {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2)`, access.ID, l.AccountID).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if !exists {
			writeError(w, http.StatusBadRequest, "invalid_account")
			return
		}
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO repeating_journal_profiles (org_id,narration,repeat_every,repeat_unit,start_date,ends_on,next_run,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$5,$7) RETURNING id`, access.ID, check.Memo, in.RepeatEvery, in.RepeatUnit, start, ends, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	for i, l := range in.Lines {
		if _, err := tx.Exec(ctx, `INSERT INTO repeating_journal_lines (org_id,profile_id,line_no,account_id,debit_cents,credit_cents,description) VALUES ($1,$2,$3,$4,$5,$6,$7)`, access.ID, id, i+1, l.AccountID, l.DebitCents, l.CreditCents, l.Description); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "next_run": day(start)})
}

func (a *app) listRepeatingJournals(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT p.id,p.narration,p.repeat_every,p.repeat_unit,p.start_date::text,COALESCE(p.ends_on::text,''),p.next_run::text,p.runs_done,p.active,
		COALESCE((SELECT sum(debit_cents) FROM repeating_journal_lines l WHERE l.org_id=p.org_id AND l.profile_id=p.id),0)
		FROM repeating_journal_profiles p WHERE p.org_id=$1 ORDER BY p.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type profile struct {
		ID          int64  `json:"id"`
		Narration   string `json:"narration"`
		RepeatEvery int    `json:"repeat_every"`
		RepeatUnit  string `json:"repeat_unit"`
		StartDate   string `json:"start_date"`
		EndsOn      string `json:"ends_on"`
		NextRun     string `json:"next_run"`
		RunsDone    int    `json:"runs_done"`
		Active      bool   `json:"active"`
		TotalCents  int64  `json:"total_cents"`
	}
	out := []profile{}
	for rows.Next() {
		var p profile
		if err := rows.Scan(&p.ID, &p.Narration, &p.RepeatEvery, &p.RepeatUnit, &p.StartDate, &p.EndsOn, &p.NextRun, &p.RunsDone, &p.Active, &p.TotalCents); err != nil {
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

// stopRepeatingJournal turns a profile off or back on. Journals already posted stay.
func (a *app) stopRepeatingJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE repeating_journal_profiles SET active=$3 WHERE org_id=$1 AND id=$2`, access.ID, in.ID, in.Active)
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

// runRepeatingJournals posts a journal for every due date up to as_of (at most 60 per profile per call).
// The org lock and the per-date journal key make a repeated run post nothing twice.
func (a *app) runRepeatingJournals(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	type due struct {
		id        int64
		runs, evr int
		unit, nar string
		start     time.Time
		next      time.Time
		ends      *time.Time
	}
	rows, err := tx.Query(ctx, `SELECT id,runs_done,repeat_every,repeat_unit,narration,start_date,next_run,ends_on
		FROM repeating_journal_profiles WHERE org_id=$1 AND active AND next_run<=$2 ORDER BY id FOR UPDATE`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.runs, &d.evr, &d.unit, &d.nar, &d.start, &d.next, &d.ends); err != nil {
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
		var lines []journalLine
		lr, err := tx.Query(ctx, `SELECT account_id,debit_cents,credit_cents,description FROM repeating_journal_lines WHERE org_id=$1 AND profile_id=$2 ORDER BY line_no`, access.ID, d.id)
		if err != nil {
			serverError(w, err)
			return
		}
		for lr.Next() {
			var l journalLine
			if err := lr.Scan(&l.AccountID, &l.DebitCents, &l.CreditCents, &l.Description); err != nil {
				lr.Close()
				serverError(w, err)
				return
			}
			lines = append(lines, l)
		}
		lr.Close()
		if err := lr.Err(); err != nil {
			serverError(w, err)
			return
		}
		for n := 0; n < 60 && !d.next.After(asOf) && (d.ends == nil || !d.next.After(*d.ends)); n++ {
			key := fmt.Sprintf("repjrnl:%d:%s", d.id, day(d.next))
			hash := sha256.Sum256([]byte(key))
			var jid int64
			err := tx.QueryRow(ctx, `INSERT INTO journals (org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
				access.ID, d.next, d.nar, key, hash[:], u.ID).Scan(&jid)
			if isPeriodClosed(err) {
				writeError(w, http.StatusUnprocessableEntity, "period_closed")
				return
			}
			if err != nil {
				serverError(w, err)
				return
			}
			for i, l := range lines {
				if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description) VALUES($1,$2,$3,$4,$5,$6,$7)`, access.ID, jid, i+1, l.AccountID, l.DebitCents, l.CreditCents, l.Description); err != nil {
					serverError(w, err)
					return
				}
			}
			created = append(created, jid)
			d.runs++
			d.next = nthRun(d.start, d.evr, d.unit, d.runs)
		}
		active := d.ends == nil || !d.next.After(*d.ends)
		if _, err := tx.Exec(ctx, `UPDATE repeating_journal_profiles SET runs_done=$3,next_run=$4,active=$5 WHERE org_id=$1 AND id=$2`, access.ID, d.id, d.runs, d.next, active); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'repeating_journal.run',jsonb_build_object('created',$3::int))`, access.ID, u.ID, len(created)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created_journal_ids": created})
}
