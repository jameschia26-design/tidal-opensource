package main

import (
	"net/http"
	"time"
)

// notifications: GET /api/v1/notifications?org_id
// The centre lists recent activity already recorded in the audit trail. Tidal does not create a second
// inbox, so nothing can be dismissed from the record itself; the page marks items read on this device.
func (a *app) notifications(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT e.id, e.action, e.created_at, COALESCE(usr.email,'')
		FROM audit_events e LEFT JOIN users usr ON usr.id=e.user_id
		WHERE e.org_id=$1 ORDER BY e.id DESC LIMIT 100`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type note struct {
		ID        int64     `json:"id"`
		Action    string    `json:"action"`
		Actor     string    `json:"actor"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := []note{}
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.ID, &n.Action, &n.CreatedAt, &n.Actor); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}
