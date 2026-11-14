package main

import (
	"net/http"
	"strings"
	"time"
)

// companyTimezone: GET or PUT /api/v1/company-timezone.
func (a *app) companyTimezone(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		var tz string
		if err := a.db.QueryRow(r.Context(), `SELECT timezone FROM organizations WHERE id=$1`, access.ID).Scan(&tz); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"timezone": tz})
		return
	}
	var in struct {
		OrgID    int64  `json:"org_id"`
		Timezone string `json:"timezone"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	in.Timezone = strings.TrimSpace(in.Timezone)
	if len(in.Timezone) < 1 || len(in.Timezone) > 64 || strings.ContainsRune(in.Timezone, 0) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_timezone")
		return
	}
	if _, err := a.db.Exec(r.Context(), `UPDATE organizations SET timezone=$2 WHERE id=$1`, access.ID, in.Timezone); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"timezone": in.Timezone})
}
