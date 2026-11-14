package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Branding themes: footer text printed on document PDFs. The default theme applies to every PDF.
// A company logo can be uploaded on the branding settings screen (backend/company_logo.go) and is
// printed top-right of the first PDF page. ponytail: no colours or per-document theme selection.

func (a *app) listBrandingThemes(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,footer,is_default FROM branding_themes WHERE org_id=$1 ORDER BY lower(name) LIMIT 100`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type theme struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Footer  string `json:"footer"`
		Default bool   `json:"is_default"`
	}
	out := []theme{}
	for rows.Next() {
		var t theme
		if err := rows.Scan(&t.ID, &t.Name, &t.Footer, &t.Default); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"themes": out})
}

func (a *app) createBrandingTheme(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID   int64  `json:"org_id"`
		Name    string `json:"name"`
		Footer  string `json:"footer"`
		Default bool   `json:"is_default"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Footer = strings.TrimSpace(in.Footer)
	if in.Name == "" || !validTaxText(in.Name, 100) || !validTaxText(in.Footer, 500) {
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
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branding_themes WHERE org_id=$1 AND lower(name)=lower($2))`, access.ID, in.Name).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "theme_name_exists")
		return
	}
	if in.Default {
		if _, err := tx.Exec(ctx, `UPDATE branding_themes SET is_default=false WHERE org_id=$1`, access.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO branding_themes(org_id,name,footer,is_default,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id`, access.ID, in.Name, in.Footer, in.Default, u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'branding_theme.created')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// updateBrandingTheme edits the name and footer of an existing theme; the default flag stays as it is.
func (a *app) updateBrandingTheme(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID  int64  `json:"org_id"`
		Name   string `json:"name"`
		Footer string `json:"footer"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Footer = strings.TrimSpace(in.Footer)
	if in.Name == "" || !validTaxText(in.Name, 100) || !validTaxText(in.Footer, 500) {
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
	// The company check comes first so another company's id reads as not found, never as a name clash.
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branding_themes WHERE org_id=$1 AND id=$2)`, access.ID, id).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var clash bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branding_themes WHERE org_id=$1 AND lower(name)=lower($2) AND id<>$3)`, access.ID, in.Name, id).Scan(&clash); err != nil {
		serverError(w, err)
		return
	}
	if clash {
		writeError(w, http.StatusConflict, "theme_name_exists")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE branding_themes SET name=$3,footer=$4 WHERE org_id=$1 AND id=$2`, access.ID, id, in.Name, in.Footer); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'branding_theme.updated')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// deleteBrandingTheme removes a theme. The default theme cannot be deleted: documents would lose their footer.
func (a *app) deleteBrandingTheme(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
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
	var isDefault bool
	err = tx.QueryRow(ctx, `SELECT is_default FROM branding_themes WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&isDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if isDefault {
		writeError(w, http.StatusConflict, "theme_is_default")
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM branding_themes WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'branding_theme.deleted')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// setDefaultBrandingTheme makes one existing theme the company default.
func (a *app) setDefaultBrandingTheme(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64 `json:"org_id"`
		ID    int64 `json:"id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ID <= 0 {
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
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branding_themes WHERE org_id=$1 AND id=$2)`, access.ID, in.ID).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE branding_themes SET is_default=false WHERE org_id=$1 AND is_default`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE branding_themes SET is_default=true WHERE org_id=$1 AND id=$2`, access.ID, in.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'branding_theme.default_changed')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID})
}
