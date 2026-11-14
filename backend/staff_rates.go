package main

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// Staff cost rates: hourly cost per company member, in SGD cents. Every change keeps its old value
// in staff_rate_history so the costing report can show when a rate moved.

const staffMembersSQL = `(SELECT COALESCE(parent_company_id,id) FROM organizations WHERE id=$1)`

func (a *app) listStaffRates(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT u.id,u.email,u.name,COALESCE(s.hourly_cents,0),
		(SELECT count(*) FROM staff_rate_history h WHERE h.org_id=m.org_id AND h.user_id=u.id),
		(SELECT to_char(max(h.effective_at),'YYYY-MM-DD"T"HH24:MI:SS') FROM staff_rate_history h WHERE h.org_id=m.org_id AND h.user_id=u.id)
		FROM memberships m JOIN users u ON u.id=m.user_id
		LEFT JOIN staff_cost_rates s ON s.org_id=m.org_id AND s.user_id=u.id WHERE m.org_id=`+staffMembersSQL+` ORDER BY u.email`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type staff struct {
		UserID      int64   `json:"user_id"`
		Email       string  `json:"email"`
		Name        string  `json:"name"`
		HourlyCents int64   `json:"hourly_cents"`
		ChangeCount int     `json:"change_count"`
		LastChanged *string `json:"last_changed_at"`
	}
	out := []staff{}
	for rows.Next() {
		var s staff
		if err := rows.Scan(&s.UserID, &s.Email, &s.Name, &s.HourlyCents, &s.ChangeCount, &s.LastChanged); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": out})
}

func (a *app) setStaffRate(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64 `json:"org_id"`
		UserID      int64 `json:"user_id"`
		HourlyCents int64 `json:"hourly_cents"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.UserID <= 0 || in.HourlyCents < 0 || in.HourlyCents > 100000000 {
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
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=`+staffMembersSQL+` AND user_id=$2)`, access.ID, in.UserID).Scan(&member); err != nil {
		serverError(w, err)
		return
	}
	if !member {
		writeError(w, http.StatusUnprocessableEntity, "invalid_staff")
		return
	}
	var prev *int64
	if err := tx.QueryRow(ctx, `SELECT hourly_cents FROM staff_cost_rates WHERE org_id=`+staffMembersSQL+` AND user_id=$2`, access.ID, in.UserID).Scan(&prev); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, err)
			return
		}
	}
	if prev == nil || *prev != in.HourlyCents {
		if _, err := tx.Exec(ctx, `INSERT INTO staff_cost_rates(org_id,user_id,hourly_cents,updated_by) SELECT `+staffMembersSQL+`,$2,$3,$4
			ON CONFLICT (org_id,user_id) DO UPDATE SET hourly_cents=$3, updated_by=$4, updated_at=now()`, access.ID, in.UserID, in.HourlyCents, u.ID); err != nil {
			serverError(w, err)
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_rate_history(org_id,user_id,hourly_cents,changed_by) SELECT `+staffMembersSQL+`,$2,$3,$4`, access.ID, in.UserID, in.HourlyCents, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": in.UserID, "hourly_cents": in.HourlyCents})
}

// staffRateHistory lists every saved rate for one member, newest first.
func (a *app) staffRateHistory(w http.ResponseWriter, r *http.Request, u currentUser) {
	userID, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT h.hourly_cents,to_char(h.effective_at,'YYYY-MM-DD"T"HH24:MI:SS'),COALESCE(c.email,'')
		FROM staff_rate_history h LEFT JOIN users c ON c.id=h.changed_by
		WHERE h.org_id=`+staffMembersSQL+` AND h.user_id=$2 ORDER BY h.effective_at DESC, h.id DESC LIMIT 200`, access.ID, userID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type change struct {
		HourlyCents int64  `json:"hourly_cents"`
		At          string `json:"at"`
		ChangedBy   string `json:"changed_by"`
	}
	out := []change{}
	for rows.Next() {
		var c change
		if err := rows.Scan(&c.HourlyCents, &c.At, &c.ChangedBy); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": out})
}
