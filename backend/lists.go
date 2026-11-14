package main

import (
	"context"
	"net/http"
	"strings"
)

// Built-in terms are not stored; saved terms add to them. Documents keep storing days, so nothing else changes.
var builtinTerms = []map[string]any{
	{"id": 0, "name": "Due on Receipt", "days": 0, "builtin": true, "active": true},
	{"id": 0, "name": "Net 15", "days": 15, "builtin": true, "active": true},
	{"id": 0, "name": "Net 30", "days": 30, "builtin": true, "active": true},
	{"id": 0, "name": "Net 45", "days": 45, "builtin": true, "active": true},
	{"id": 0, "name": "Net 60", "days": 60, "builtin": true, "active": true},
}

func (a *app) listSalespeople(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,email,active FROM salespeople WHERE org_id=$1 ORDER BY lower(name) LIMIT 500`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, email string
		var active bool
		if err := rows.Scan(&id, &name, &email, &active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "email": email, "active": active})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"salespeople": out})
}

func (a *app) createSalesperson(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Email = strings.TrimSpace(in.Name), strings.TrimSpace(in.Email)
	if in.Name == "" || !validTaxText(in.Name, 200) || !validTaxText(in.Email, 254) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO salespeople(org_id,name,email) VALUES($1,$2,$3) RETURNING id`, access.ID, in.Name, in.Email).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) listPaymentTerms(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,days,active FROM payment_terms WHERE org_id=$1 ORDER BY days,lower(name) LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := append([]map[string]any{}, builtinTerms...)
	for rows.Next() {
		var id int64
		var name string
		var days int
		var active bool
		if err := rows.Scan(&id, &name, &days, &active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "days": days, "builtin": false, "active": active})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"payment_terms": out})
}

func (a *app) createPaymentTerm(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
		Days  int    `json:"days"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) || in.Days < 0 || in.Days > 365 {
		writeError(w, 400, "invalid_input")
		return
	}
	for _, t := range builtinTerms {
		if strings.EqualFold(t["name"].(string), in.Name) {
			writeError(w, 409, "extras_exists")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO payment_terms(org_id,name,days) VALUES($1,$2,$3) RETURNING id`, access.ID, in.Name, in.Days).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// Built-in modes are stored by key; company modes are stored by their name, which may not equal a key.
var builtinModes = []map[string]any{
	{"value": "cash", "label": "Cash"}, {"value": "bank_transfer", "label": "Bank transfer"}, {"value": "cheque", "label": "Cheque"},
	{"value": "card", "label": "Card"}, {"value": "paynow", "label": "PayNow"}, {"value": "other", "label": "Other"},
}

func builtinMode(m string) bool {
	for _, b := range builtinModes {
		if b["value"] == m {
			return true
		}
	}
	return false
}

// validPaymentMode accepts a built-in key or an active company mode. Old documents keep modes that were switched off.
func (a *app) validPaymentMode(ctx context.Context, orgID int64, mode string) bool {
	if builtinMode(mode) {
		return true
	}
	var ok bool
	return a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_modes WHERE org_id=$1 AND name=$2 AND active)`, orgID, mode).Scan(&ok) == nil && ok
}

func (a *app) listPaymentModes(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,active FROM payment_modes WHERE org_id=$1 ORDER BY lower(name) LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for _, b := range builtinModes {
		out = append(out, map[string]any{"id": 0, "value": b["value"], "label": b["label"], "builtin": true, "active": true})
	}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		if err := rows.Scan(&id, &name, &active); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "value": name, "label": name, "builtin": false, "active": active})
	}
	if rows.Err() != nil {
		serverError(w, rows.Err())
		return
	}
	writeJSON(w, 200, map[string]any{"payment_modes": out})
}

func (a *app) createPaymentMode(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 50) {
		writeError(w, 400, "invalid_input")
		return
	}
	for _, b := range builtinModes {
		if strings.EqualFold(b["label"].(string), in.Name) || strings.EqualFold(b["value"].(string), in.Name) {
			writeError(w, 409, "extras_exists")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO payment_modes(org_id,name) VALUES($1,$2) RETURNING id`, access.ID, in.Name).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "extras_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}
