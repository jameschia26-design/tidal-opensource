package main

import (
	"net/http"
	"strings"
)

// listProjects returns projects, optionally for one customer (contact_id).
func (a *app) listProjects(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var contact int64
	if v := r.URL.Query().Get("contact_id"); v != "" {
		var okc bool
		if contact, okc = parseOrgID(w, v); !okc {
			return
		}
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,contact_id,name,active,estimated_cost_cents,estimated_charge_cents FROM projects WHERE org_id=$1 AND ($2=0 OR contact_id=$2) ORDER BY lower(name) LIMIT 500`, access.ID, contact)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, cid, estCost, estCharge int64
		var name string
		var active bool
		if err := rows.Scan(&id, &cid, &name, &active, &estCost, &estCharge); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "contact_id": cid, "name": name, "active": active, "estimated_cost_cents": estCost, "estimated_charge_cents": estCharge})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"projects": out})
}

func (a *app) createProject(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID           int64  `json:"org_id"`
		ContactID       int64  `json:"contact_id"`
		Name            string `json:"name"`
		EstimatedCost   int64  `json:"estimated_cost_cents"`
		EstimatedCharge int64  `json:"estimated_charge_cents"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.ContactID <= 0 || !validTaxText(in.Name, 200) || in.EstimatedCost < 0 || in.EstimatedCharge < 0 || in.EstimatedCost > maxMoneyCents || in.EstimatedCharge > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var kind string
	if err := a.db.QueryRow(r.Context(), `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind); err != nil || kind == "supplier" {
		writeError(w, 422, "invalid_contact")
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO projects(org_id,contact_id,name,estimated_cost_cents,estimated_charge_cents) VALUES($1,$2,$3,$4,$5) RETURNING id`, access.ID, in.ContactID, in.Name, in.EstimatedCost, in.EstimatedCharge).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// updateProject: PATCH /api/v1/projects/{id} {org_id, estimated_cost_cents, estimated_charge_cents}
func (a *app) updateProject(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID           int64 `json:"org_id"`
		EstimatedCost   int64 `json:"estimated_cost_cents"`
		EstimatedCharge int64 `json:"estimated_charge_cents"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.EstimatedCost < 0 || in.EstimatedCharge < 0 || in.EstimatedCost > maxMoneyCents || in.EstimatedCharge > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	cmd, err := a.db.Exec(r.Context(), `UPDATE projects SET estimated_cost_cents=$3, estimated_charge_cents=$4 WHERE org_id=$1 AND id=$2`, access.ID, id, in.EstimatedCost, in.EstimatedCharge)
	if err != nil {
		serverError(w, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		writeError(w, 404, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "estimated_cost_cents": in.EstimatedCost, "estimated_charge_cents": in.EstimatedCharge})
}
