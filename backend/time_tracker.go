package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// timerView is the running timer for the signed-in user, in the shape the time page reads.
type timerView struct {
	OrgID       int64  `json:"org_id"`
	ProjectID   int64  `json:"project_id"`
	Project     string `json:"project"`
	Task        string `json:"task"`
	Description string `json:"description"`
	Billable    bool   `json:"billable"`
	RateCents   int64  `json:"rate_cents"`
	StartedAt   string `json:"started_at"`
}

// getTimer: GET /api/v1/time/timer?org_id — 404 when nothing is running.
func (a *app) getTimer(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var v timerView
	err := a.db.QueryRow(r.Context(), `SELECT t.org_id,t.project_id,p.name,t.task,t.description,t.billable,t.rate_cents,t.started_at
		FROM time_timers t JOIN projects p ON p.org_id=t.org_id AND p.id=t.project_id WHERE t.user_id=$1 AND t.org_id=$2`, u.ID, access.ID).
		Scan(&v.OrgID, &v.ProjectID, &v.Project, &v.Task, &v.Description, &v.Billable, &v.RateCents, &v.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"timer": nil})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"timer": v})
}

// startTimer: POST /api/v1/time/timer/start — one running timer per user; starting again replaces it.
func (a *app) startTimer(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64  `json:"org_id"`
		ProjectID   int64  `json:"project_id"`
		Task        string `json:"task"`
		Description string `json:"description"`
		Billable    bool   `json:"billable"`
		RateCents   int64  `json:"rate_cents"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Task = strings.TrimSpace(in.Task)
	if in.ProjectID <= 0 || in.Task == "" || !validTaxText(in.Task, 200) || !validTaxText(in.Description, 1000) || in.RateCents < 0 || in.RateCents > maxMoneyCents {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var project string
	if err := a.db.QueryRow(r.Context(), `SELECT name FROM projects WHERE org_id=$1 AND id=$2 AND active`, access.ID, in.ProjectID).Scan(&project); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_project")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO time_timers(user_id,org_id,project_id,task,description,billable,rate_cents,started_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT (user_id) DO UPDATE SET org_id=$2,project_id=$3,task=$4,description=$5,billable=$6,rate_cents=$7,started_at=now()`,
		u.ID, access.ID, in.ProjectID, in.Task, in.Description, in.Billable, in.RateCents); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project_id": in.ProjectID, "project": project})
}

// stopTimer: POST /api/v1/time/timer/stop — turns the running timer into a normal time entry, so
// project and staff reports keep reading one table. A sub-minute stop counts as one minute.
func (a *app) stopTimer(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	tx, err := a.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var projectID, rate int64
	var task, description string
	var billable bool
	var started time.Time
	err = tx.QueryRow(r.Context(), `SELECT project_id,task,description,billable,rate_cents,started_at FROM time_timers WHERE user_id=$1 AND org_id=$2 FOR UPDATE`, u.ID, access.ID).
		Scan(&projectID, &task, &description, &billable, &rate, &started)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no_timer")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	minutes := int(time.Since(started).Minutes())
	if minutes < 1 {
		minutes = 1
	}
	if minutes > 1440 {
		minutes = 1440
	}
	var id int64
	if err := tx.QueryRow(r.Context(), `INSERT INTO time_entries(org_id,project_id,entry_date,task,description,minutes,created_by,billable,rate_cents)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		access.ID, projectID, day(started), task, description, minutes, u.ID, billable, rate).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM time_timers WHERE user_id=$1`, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "minutes": minutes, "billable": billable, "rate_cents": rate})
}
