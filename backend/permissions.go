package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// canPostDocuments answers whether a member may post documents in one company. Roles decide the
// default — viewers never post — and an explicit member override can only take that right away, so a
// permission change cannot hand a viewer write access.
func canPostDocuments(ctx context.Context, q rowQueryer, orgID, userID int64) (bool, error) {
	var role, override string
	err := q.QueryRow(ctx, `SELECT m.role, COALESCE(m.permissions->>'post_documents','') FROM memberships m WHERE m.org_id=COALESCE((SELECT parent_company_id FROM organizations WHERE id=$1),$1) AND m.user_id=$2`, orgID, userID).Scan(&role, &override)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if role == "viewer" {
		return false, nil
	}
	return override != "false", nil
}

// featureAllowed answers whether a member may write one feature area. Roles decide the default —
// viewers never write — and an explicit per-feature false takes that right away. Features are named
// after the area of the books they guard, so one member may keep sales while losing purchases.
func featureAllowed(ctx context.Context, q rowQueryer, orgID, userID int64, feature string) (bool, error) {
	var role, override string
	err := q.QueryRow(ctx, `SELECT m.role, COALESCE(m.permissions->>$2,'') FROM memberships m WHERE m.org_id=COALESCE((SELECT parent_company_id FROM organizations WHERE id=$1),$1) AND m.user_id=$3`, orgID, feature, userID).Scan(&role, &override)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if role == "viewer" {
		return false, nil
	}
	return override != "false", nil
}

// requireFeature refuses a write when the member has lost that feature. It answers true when the
// caller may continue, and writes the refusal itself.
func (a *app) requireFeature(w http.ResponseWriter, ctx context.Context, orgID, userID int64, feature string) bool {
	allowed, err := featureAllowed(ctx, a.db, orgID, userID, feature)
	if err != nil {
		serverError(w, err)
		return false
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

// setMemberPermissions: PATCH /api/v1/members/{id}/permissions {org_id, post_documents}
func (a *app) setMemberPermissions(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID        int64           `json:"org_id"`
		PostDocument *bool           `json:"post_documents"`
		Permissions  map[string]bool `json:"permissions"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.PostDocument == nil && len(in.Permissions) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	matrix := in.Permissions
	if matrix == nil {
		matrix = map[string]bool{}
	}
	if in.PostDocument != nil {
		matrix["post_documents"] = *in.PostDocument
	}
	validFeatures := map[string]bool{"post_documents": true, "sales": true, "purchases": true, "banking": true, "contacts": true, "items": true, "reports": true, "settings": true}
	for key := range matrix {
		if !validFeatures[key] {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	company, _, err := lockCompanyManager(ctx, tx, in.OrgID, u.ID)
	if err != nil {
		managerError(w, err)
		return
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2 FOR UPDATE`, company, id).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	if role == "owner" || (id == u.ID && role == "admin") {
		// Xero refuses this too: an admin cannot lift or drop their own rights.
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	payload, err := json.Marshal(matrix)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE memberships SET permissions = permissions || $3::jsonb WHERE org_id=$1 AND user_id=$2`, company, id, payload); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id, user_id, action, detail) VALUES($1,$2,'member.permissions_changed', jsonb_build_object('member_id',$3::bigint,'permissions',$4::jsonb))`, company, u.ID, id, payload); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
