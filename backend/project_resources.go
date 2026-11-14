package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type projectTask struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	RateCents int64  `json:"rate_cents"`
	Active    bool   `json:"active"`
}

// projectTasks: GET /api/v1/projects/{id}/tasks?org_id
func (a *app) projectTasks(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,rate_cents,active FROM project_tasks WHERE org_id=$1 AND project_id=$2 ORDER BY lower(name),id`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []projectTask{}
	for rows.Next() {
		var v projectTask
		if err := rows.Scan(&v.ID, &v.Name, &v.RateCents, &v.Active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// saveProjectTask: POST /api/v1/projects/{id}/tasks, PATCH /api/v1/project-tasks/{id}.
func (a *app) saveProjectTask(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID     int64  `json:"org_id"`
		Name      string `json:"name"`
		RateCents int64  `json:"rate_cents"`
		Active    *bool  `json:"active"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if r.Method == http.MethodPatch {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" || len(name) > 200 || in.RateCents < 0 || in.Active == nil {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		if _, err := a.db.Exec(r.Context(), `UPDATE project_tasks SET name=$2,rate_cents=$3,active=$4 WHERE org_id=$1 AND id=$5`, access.ID, name, in.RateCents, *in.Active, id); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	project, ok := pathID(w, r)
	if !ok {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 || in.RateCents < 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var id int64
	err := a.db.QueryRow(r.Context(), `INSERT INTO project_tasks(org_id,project_id,name,rate_cents) VALUES($1,$2,$3,$4) RETURNING id`, access.ID, project, in.Name, in.RateCents).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "task_exists")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// projectMembers: GET /api/v1/projects/{id}/members?org_id
func (a *app) projectMembers(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT m.user_id, COALESCE(NULLIF(u.name,''),u.email), m.role FROM project_members m
		JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.project_id=$2 ORDER BY m.role, lower(u.email)`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var uid int64
		var name, role string
		if err := rows.Scan(&uid, &name, &role); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"user_id": uid, "name": name, "role": role})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

// saveProjectMember: POST /api/v1/projects/{id}/members {org_id,user_id,role}, DELETE with a user id in
// the path. Only someone already in the company can be added to a project.
func (a *app) saveProjectMember(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		user, ok := parseOrgID(w, r.URL.Query().Get("user_id"))
		if !ok || user == 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, true)
		if !ok {
			return
		}
		if _, err := a.db.Exec(r.Context(), `DELETE FROM project_members WHERE org_id=$1 AND project_id=$2 AND user_id=$3`, access.ID, id, user); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	var in struct {
		OrgID  int64  `json:"org_id"`
		UserID int64  `json:"user_id"`
		Role   string `json:"role"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Role == "" {
		in.Role = "member"
	}
	if in.UserID <= 0 || (in.Role != "lead" && in.Role != "member") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var member bool
	if err := a.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=$1 AND user_id=$2)`, access.ID, in.UserID).Scan(&member); err != nil {
		serverError(w, err)
		return
	}
	if !member {
		writeError(w, http.StatusUnprocessableEntity, "not_a_member")
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO project_members(org_id,project_id,user_id,role) VALUES($1,$2,$3,$4)
		ON CONFLICT (org_id,project_id,user_id) DO UPDATE SET role=$4`, access.ID, id, in.UserID, in.Role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
