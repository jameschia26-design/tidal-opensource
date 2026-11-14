package main

import (
	"net/http"
	"strings"
)

// recordLogin appends one login event. It is called only when a session is actually issued, so the
// history means "signed in", not "tried to sign in". Failures are ignored on purpose: a bookkeeping
// row must never block a valid sign-in.
func (a *app) recordLogin(r *http.Request, userID int64) {
	ip := r.RemoteAddr
	if host, _, ok := strings.Cut(r.RemoteAddr, ":"); ok {
		ip = host
	}
	if len(ip) > 64 {
		ip = ip[:64]
	}
	agent := r.UserAgent()
	if len(agent) > 300 {
		agent = agent[:300]
	}
	_, _ = a.db.Exec(r.Context(), `INSERT INTO login_events(user_id, ip, user_agent) VALUES($1,$2,$3)`, userID, ip, agent)
}

// loginHistory: GET /api/v1/login-history — the caller's own recent sign-ins.
func (a *app) loginHistory(w http.ResponseWriter, r *http.Request, u currentUser) {
	rows, err := a.db.Query(r.Context(), `SELECT occurred_at, ip, user_agent FROM login_events WHERE user_id=$1 ORDER BY occurred_at DESC LIMIT 20`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var at any
		var ip, agent string
		if err := rows.Scan(&at, &ip, &agent); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"occurred_at": at, "ip": ip, "user_agent": agent})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
