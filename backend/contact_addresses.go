package main

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxExtraAddresses = 20

// contactAddresses lists and adds extra named addresses for one customer or supplier.
func (a *app) contactAddresses(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, r.Method != http.MethodGet)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var one int
	if err := a.db.QueryRow(ctx, `SELECT 1 FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&one); err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		rows, err := a.db.Query(ctx, `SELECT id,label,address FROM contact_addresses WHERE org_id=$1 AND contact_id=$2 ORDER BY id`, access.ID, id)
		if err != nil {
			serverError(w, err)
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var aid int64
			var label string
			var addr contactAddress
			if err := rows.Scan(&aid, &label, &addr); err != nil {
				serverError(w, err)
				return
			}
			out = append(out, map[string]any{"id": aid, "label": label, "address": addr})
		}
		if err := rows.Err(); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"addresses": out})
		return
	}
	var in struct {
		Label   string         `json:"label"`
		Address contactAddress `json:"address"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Label = strings.TrimSpace(in.Label)
	if in.Label == "" || utf8.RuneCountInString(in.Label) > 100 || !validTaxText(in.Label, 100) || !validAddress(in.Address) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	// ponytail: count-then-insert can overshoot the cap by a few on concurrent adds; harmless for a soft limit.
	var n int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM contact_addresses WHERE org_id=$1 AND contact_id=$2`, access.ID, id).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	if n >= maxExtraAddresses {
		writeError(w, http.StatusUnprocessableEntity, "address_limit")
		return
	}
	var aid int64
	if err := a.db.QueryRow(ctx, `INSERT INTO contact_addresses(org_id,contact_id,label,address) VALUES($1,$2,$3,$4) RETURNING id`, access.ID, id, in.Label, in.Address).Scan(&aid); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": aid})
}

func (a *app) deleteContactAddress(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM contact_addresses WHERE org_id=$1 AND id=$2`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
