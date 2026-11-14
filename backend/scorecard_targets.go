package main

import (
	"encoding/json"
	"net/http"
)

// scorecardTargets: GET/PUT /api/v1/scorecard-targets {org_id, targets}
// Targets are company settings, not report parameters, so the scorecard reads the same thresholds for
// everyone and a saved report cannot change what "healthy" means.
func (a *app) scorecardTargets(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, orgID, false)
		if !ok {
			return
		}
		var raw []byte
		if err := a.db.QueryRow(r.Context(), `SELECT scorecard_targets FROM organizations WHERE id=$1`, access.ID).Scan(&raw); err != nil {
			serverError(w, err)
			return
		}
		out := map[string]int64{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		writeJSON(w, http.StatusOK, map[string]any{"targets": out})
		return
	}
	var in struct {
		OrgID   int64            `json:"org_id"`
		Targets map[string]int64 `json:"targets"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	allowed := map[string]bool{"net_margin_bp": true, "current_ratio_bp": true, "debtor_days": true, "creditor_days": true, "cash_runway_months": true}
	for key, value := range in.Targets {
		if !allowed[key] || value < 0 || value > 10000000 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	payload, _ := json.Marshal(in.Targets)
	if _, err := a.db.Exec(r.Context(), `UPDATE organizations SET scorecard_targets=$2 WHERE id=$1`, access.ID, payload); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": in.Targets})
}
