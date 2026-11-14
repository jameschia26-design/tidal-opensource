package main

import (
	"net/http"
	"strconv"
	"time"
)

type eventRow struct {
	ID         int64     `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
}

// ponytail: company audit events only; add external event sources when integrations exist.
func (a *app) listEvents(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var before int64
	if value := r.URL.Query().Get("before_id"); value != "" {
		var err error
		before, err = strconv.ParseInt(value, 10, 64)
		if err != nil || before <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT id, created_at, action FROM audit_events WHERE org_id=$1 AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT 100`, access.ID, before)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []eventRow{}
	for rows.Next() {
		var row eventRow
		if err := rows.Scan(&row.ID, &row.OccurredAt, &row.Type); err != nil {
			serverError(w, err)
			return
		}
		row.Source = "Tidal"
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
