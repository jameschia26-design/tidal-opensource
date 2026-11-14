package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// exportCompany: GET /api/v1/export/company?org_id — a full JSON archive of every org-scoped table (R17).
// It is an archive for the customer, not a migration back into Tidal: attachment bytes, user logins and
// secrets are excluded, and re-import is not supported. See docs/migration-import.md.
func (a *app) exportCompany(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := a.db.Query(ctx, `SELECT DISTINCT table_name FROM information_schema.columns
		WHERE table_schema='public' AND column_name='org_id' ORDER BY table_name`)
	if err != nil {
		serverError(w, err)
		return
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			serverError(w, err)
			return
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"org_id": access.ID, "tables": map[string]any{}}
	store := out["tables"].(map[string]any)
	for _, table := range tables {
		data, err := a.exportTable(ctx, access.ID, table)
		if err != nil {
			serverError(w, err)
			return
		}
		store[table] = data
	}
	writeJSON(w, http.StatusOK, out)
}

// exportTable reads one org-scoped table as raw JSON rows. Column names come from the database, so new
// columns are exported without code changes.
func (a *app) exportTable(ctx context.Context, orgID int64, table string) ([]map[string]any, error) {
	rows, err := a.db.Query(ctx, `SELECT row_to_json(t) FROM (SELECT * FROM `+pgQuoteIdent(table)+` WHERE org_id=$1 ORDER BY 1) t`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// pgQuoteIdent quotes a table name read from information_schema.
func pgQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
