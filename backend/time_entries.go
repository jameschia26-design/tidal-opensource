package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ponytail: tasks are free text; link to project tasks when task management exists.
func (a *app) createTimeEntry(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64  `json:"org_id"`
		ProjectID   int64  `json:"project_id"`
		Date        string `json:"date"`
		Task        string `json:"task"`
		Description string `json:"description"`
		Minutes     int    `json:"minutes"`
		Billable    bool   `json:"billable"`
		RateCents   int64  `json:"rate_cents"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Task = strings.TrimSpace(in.Task)
	if in.ProjectID <= 0 || in.Task == "" || !validTaxText(in.Task, 200) || !validTaxText(in.Description, 1000) || in.Minutes < 1 || in.Minutes > 1440 || in.RateCents < 0 || in.RateCents > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	date, ok := parseDay(in.Date)
	if !ok {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	err := a.db.QueryRow(r.Context(), `INSERT INTO time_entries(org_id,project_id,entry_date,task,description,minutes,created_by,billable,rate_cents) SELECT $1,id,$3,$4,$5,$6,$7,$8,$9 FROM projects WHERE org_id=$1 AND id=$2 AND active RETURNING id`, access.ID, in.ProjectID, date, in.Task, in.Description, in.Minutes, u.ID, in.Billable, in.RateCents).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 422, "invalid_project")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) listTimeEntries(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	var from, to any
	if r.URL.Query().Has("from") || r.URL.Query().Has("to") {
		start, end, valid := ledgerRange(w, r)
		if !valid {
			return
		}
		from, to = start, end
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT t.id,t.project_id,p.name,p.active,t.entry_date::text,t.task,t.description,t.minutes,COALESCE(NULLIF(u.name,''),u.email),u.id,t.billable,t.rate_cents FROM time_entries t JOIN projects p ON p.org_id=t.org_id AND p.id=t.project_id JOIN users u ON u.id=t.created_by WHERE t.org_id=$1 AND ($2::date IS NULL OR t.entry_date BETWEEN $2 AND $3) ORDER BY t.entry_date DESC,t.id DESC LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, pid, recorderID int64
		var project, date, task, description, staff string
		var active bool
		var minutes int
		var billable bool
		var rate int64
		if err := rows.Scan(&id, &pid, &project, &active, &date, &task, &description, &minutes, &staff, &recorderID, &billable, &rate); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "project_id": pid, "project": project, "project_status": map[bool]string{true: "active", false: "closed"}[active], "date": date, "task": task, "description": description, "minutes": minutes, "staff": staff, "recorder_id": recorderID, "billable": billable, "rate_cents": rate, "value_cents": rate * int64(minutes) / 60})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	truncated := len(out) > 500
	if truncated {
		out = out[:500]
	}
	writeJSON(w, 200, map[string]any{"entries": out, "truncated": truncated})
}
