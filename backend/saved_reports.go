package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type savedReport struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	ReportKey string          `json:"report_key"`
	Status    string          `json:"status"`
	Params    json.RawMessage `json:"params"`
	Layout    json.RawMessage `json:"layout"`
	Favourite bool            `json:"favourite"`
}

const blankReportKey = "blank"

// listSavedReports: GET /api/v1/saved-reports?org_id&report_key&status
func (a *app) listSavedReports(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("report_key"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if len(key) > 60 || (status != "" && status != "draft" && status != "published" && status != "archived") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,title,report_key,status,params,layout,favourite FROM saved_reports
		WHERE org_id=$1 AND ($2='' OR report_key=$2) AND ($3='' OR status=$3) ORDER BY favourite DESC,lower(title), id LIMIT 200`, access.ID, key, status)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []savedReport{}
	for rows.Next() {
		var v savedReport
		if err := rows.Scan(&v.ID, &v.Title, &v.ReportKey, &v.Status, &v.Params, &v.Layout, &v.Favourite); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved_reports": out})
}

// saveSavedReport: POST /api/v1/saved-reports (create), PUT /api/v1/saved-reports/{id} (update).
func (a *app) saveSavedReport(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		ID        int64           `json:"id"`
		OrgID     int64           `json:"org_id"`
		Title     string          `json:"title"`
		ReportKey string          `json:"report_key"`
		Status    string          `json:"status"`
		Params    json.RawMessage `json:"params"`
		Layout    json.RawMessage `json:"layout"`
		Favourite *bool           `json:"favourite"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.ReportKey = strings.TrimSpace(in.ReportKey)
	if in.Status == "" {
		in.Status = "draft"
	}
	if in.Title == "" || len(in.Title) > 100 || in.ReportKey == "" || len(in.ReportKey) > 60 ||
		(in.Status != "draft" && in.Status != "published" && in.Status != "archived") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if len(in.Params) == 0 {
		in.Params = json.RawMessage(`{}`)
	}
	if len(in.Layout) == 0 {
		in.Layout = json.RawMessage(`{}`)
	}
	if !json.Valid(in.Params) || !json.Valid(in.Layout) || in.Params[0] != '{' || in.Layout[0] != '{' {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if r.Method == http.MethodPut {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var orgID int64
		if err := a.db.QueryRow(r.Context(), `UPDATE saved_reports SET title=$2,report_key=$3,status=$4,params=$5,layout=$6,favourite=COALESCE($7,favourite),updated_at=now()
			WHERE org_id=$1 AND id=$8 RETURNING org_id`, access.ID, in.Title, in.ReportKey, in.Status, in.Params, in.Layout, in.Favourite, id).Scan(&orgID); err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO saved_reports(org_id,title,report_key,status,params,layout,favourite,created_by)
		VALUES($1,$2,$3,$4,$5,$6,COALESCE($7,false),$8) RETURNING id`, access.ID, in.Title, in.ReportKey, in.Status, in.Params, in.Layout, in.Favourite, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// deleteSavedReport: DELETE /api/v1/saved-reports/{id} — only a draft can be deleted; published
// reports are archived instead, so a report someone relied on cannot vanish.
func (a *app) deleteSavedReport(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
		return
	}
	var status string
	if err := a.db.QueryRow(r.Context(), `SELECT status FROM saved_reports WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if status != "draft" {
		writeError(w, http.StatusConflict, "published_report")
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM saved_reports WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

// blankReport: GET /api/v1/reports/blank?org_id&saved_id — a user-defined column and row layout. A
// saved blank report is returned exactly as stored; without one, empty columns are returned so the
// page can start a new report.
func (a *app) blankReport(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	out := map[string]any{"columns": []any{}, "rows": []any{}, "title": "Blank report"}
	if raw := r.URL.Query().Get("saved_id"); raw != "" {
		id, valid := parseOrgID(w, raw)
		if !valid {
			return
		}
		var title string
		var layout []byte
		if err := a.db.QueryRow(r.Context(), `SELECT title, layout FROM saved_reports WHERE org_id=$1 AND id=$2 AND report_key=$3`, access.ID, id, blankReportKey).Scan(&title, &layout); err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		var parsed struct {
			Columns []string   `json:"columns"`
			Rows    [][]string `json:"rows"`
		}
		if err := json.Unmarshal(layout, &parsed); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_layout")
			return
		}
		out["title"] = title
		out["columns"] = parsed.Columns
		if parsed.Rows != nil {
			out["rows"] = parsed.Rows
		}
	}
	writeJSON(w, http.StatusOK, out)
}
