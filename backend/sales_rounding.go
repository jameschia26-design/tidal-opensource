package main

import "net/http"

// salesRounding reads or sets the company rounding rule. The forms use it to suggest a rounding adjustment;
// the document total stays whatever the saved adjustment makes it, so old documents never change.
func (a *app) salesRounding(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID    int64  `json:"org_id"`
		Rounding string `json:"rounding"`
	}
	write := r.Method == http.MethodPut
	if write {
		if !decodeJSON(w, r, &in) {
			return
		}
	} else {
		var ok bool
		if in.OrgID, ok = parseOrgID(w, r.URL.Query().Get("org_id")); !ok {
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, write)
	if !ok {
		return
	}
	if write {
		switch in.Rounding {
		case "none", "whole", "5c", "10c":
		default:
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		if _, err := a.db.Exec(r.Context(), `UPDATE organizations SET sales_rounding=$2 WHERE id=$1`, access.ID, in.Rounding); err != nil {
			serverError(w, err)
			return
		}
	}
	var current string
	if err := a.db.QueryRow(r.Context(), `SELECT sales_rounding FROM organizations WHERE id=$1`, access.ID).Scan(&current); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rounding": current})
}
