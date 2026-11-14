package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

var inviteRoles = map[string]bool{"admin": true, "accountant": true, "member": true, "viewer": true}

type memberInviteInput struct {
	OrgID     int64  `json:"org_id"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func (a *app) inviteMember(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in memberInviteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	email, emailOK := normalizeEmail(in.Email)
	if !emailOK || !inviteRoles[in.Role] {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	for _, name := range []string{in.FirstName, in.LastName} {
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsRune(name, 0) {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if in.Role == "admin" && access.Role != "owner" {
		// Matches Xero: only an owner can create another administrator.
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
	companyID, actorRole, err := lockCompanyManager(ctx, tx, access.ID, u.ID)
	if err != nil {
		managerError(w, err)
		return
	}
	if in.Role == "admin" && actorRole != "owner" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND u.email=$2)`, companyID, email).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "already_member")
		return
	}
	var pending bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM invitations WHERE email=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now())`, email, companyID).Scan(&pending); err != nil {
		serverError(w, err)
		return
	}
	if pending {
		writeError(w, http.StatusConflict, "invite_pending")
		return
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO invitations (email,tier,org_id,role,created_by,expires_at,first_name,last_name)
		 VALUES ($1, 'essential', $2, $3, $4, now() + make_interval(secs => $5), $6, $7)
		 RETURNING id`,
		email, companyID, in.Role, u.ID, invitationTTL.Seconds(), in.FirstName, in.LastName).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	// Send before commit: a delivery failure rolls the invitation back so it can be retried.
	var orgName string
	if err := tx.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, companyID).Scan(&orgName); err != nil {
		serverError(w, err)
		return
	}
	body := "You have been invited to " + orgName + " in Tidal. Open " + a.cfg.PublicOrigin + "/invitations, sign in or register and verify this email address, then explicitly accept the invitation. The invitation expires in 7 days."
	if err := a.mail.send(email, "You are invited to "+orgName+" on Tidal", body); err != nil {
		log.Printf("company invitation email not sent: %v", err)
		writeError(w, http.StatusServiceUnavailable, "email_unavailable")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (org_id,user_id,action,detail) VALUES ($1,$2,'member.invited',jsonb_build_object('invitation',$3::bigint,'role',$4::text))`, companyID, u.ID, id, in.Role); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "invited", "invitation_id": id})
}

// listMemberInvites shows pending company invitations so an owner can see who was asked.
func (a *app) listMemberInvites(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,email,role,first_name,last_name,expires_at::text FROM invitations WHERE org_id=(SELECT COALESCE(parent_company_id,id) FROM organizations WHERE id=$1) AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now() ORDER BY email`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type invite struct {
		ID        int64  `json:"id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		ExpiresAt string `json:"expires_at"`
	}
	out := []invite{}
	for rows.Next() {
		var v invite
		if err := rows.Scan(&v.ID, &v.Email, &v.Role, &v.FirstName, &v.LastName, &v.ExpiresAt); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

// revokeMemberInvite cancels a pending company invitation.
func (a *app) revokeMemberInvite(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	companyID, role, err := lockCompanyManager(ctx, tx, access.ID, u.ID)
	if err != nil {
		managerError(w, err)
		return
	}
	var inviteRole string
	err = tx.QueryRow(ctx, `SELECT role FROM invitations WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL FOR UPDATE`, id, companyID).Scan(&inviteRole)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if inviteRole == "admin" && role != "owner" {
		writeError(w, 403, "forbidden")
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE invitations SET revoked_at=now() WHERE id=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'member.invitation_revoked',jsonb_build_object('invitation',$3::bigint))`, companyID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Serialize all access changes on the company, including requests made through a child book.
func lockCompanyManager(ctx context.Context, tx pgx.Tx, orgID, userID int64) (int64, string, error) {
	var companyID int64
	var role string
	err := tx.QueryRow(ctx, `SELECT p.id FROM organizations o JOIN organizations p ON p.id=COALESCE(o.parent_company_id,o.id) WHERE o.id=$1 AND o.status='active' AND p.status='active' AND NOT p.is_shared_sample FOR UPDATE OF p`, orgID).Scan(&companyID)
	if err != nil {
		return 0, "", err
	}
	err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2`, companyID, userID).Scan(&role)
	if err != nil {
		return 0, "", err
	}
	if role != "owner" && role != "admin" {
		return 0, "", pgx.ErrNoRows
	}
	return companyID, role, nil
}

func (a *app) myInvitations(w http.ResponseWriter, r *http.Request, u currentUser) {
	rows, err := a.db.Query(r.Context(), `SELECT i.id,i.org_id,o.name,i.role,i.expires_at::text FROM invitations i JOIN organizations o ON o.id=i.org_id JOIN users u ON u.email=i.email WHERE u.id=$1 AND u.email_verified_at IS NOT NULL AND NOT u.disabled AND o.status='active' AND o.parent_company_id IS NULL AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at>now() ORDER BY i.id`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, org int64
		var name, role, expires string
		if err := rows.Scan(&id, &org, &name, &role, &expires); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, map[string]any{"id": id, "org_id": org, "org_name": name, "role": role, "expires_at": expires})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"invitations": out})
}

func (a *app) acceptInvitation(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Lock company first, consistently with management operations.
	var orgID int64
	err = tx.QueryRow(ctx, `SELECT o.id FROM invitations i JOIN organizations o ON o.id=i.org_id JOIN users u ON u.email=i.email WHERE i.id=$1 AND u.id=$2 AND u.email_verified_at IS NOT NULL AND NOT u.disabled AND o.status='active' AND o.parent_company_id IS NULL FOR UPDATE OF o`, id, u.ID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM invitations WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at>now() FOR UPDATE`, id, orgID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, orgID, u.ID, role)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "already_member")
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE invitations SET accepted_at=now() WHERE id=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'member.invitation_accepted',jsonb_build_object('invitation',$3::bigint,'role',$4::text))`, orgID, u.ID, id, role); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "accepted", "org_id": orgID})
}

func (a *app) changeMember(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	role := ""
	if r.Method == http.MethodPatch {
		var in struct {
			Role string `json:"role"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		role = in.Role
		if !inviteRoles[role] {
			writeError(w, 400, "invalid_input")
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
	company, actor, err := lockCompanyManager(ctx, tx, org, u.ID)
	if err != nil {
		managerError(w, err)
		return
	}
	var old string
	err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2 FOR UPDATE`, company, id).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if old == "owner" || (actor != "owner" && (old == "admin" || role == "admin")) {
		writeError(w, 403, "forbidden")
		return
	}
	action := "member.removed"
	if r.Method == http.MethodPatch {
		action = "member.role_changed"
		_, err = tx.Exec(ctx, `UPDATE memberships SET role=$3 WHERE org_id=$1 AND user_id=$2`, company, id, role)
	} else {
		_, err = tx.Exec(ctx, `WITH revoked AS (
			UPDATE invitations SET revoked_at=now()
			WHERE org_id=$1 AND lower(btrim(email))=(SELECT lower(btrim(email)) FROM users WHERE id=$2)
			  AND accepted_at IS NULL AND revoked_at IS NULL
			RETURNING id
		) INSERT INTO audit_events(org_id,user_id,action,detail)
		  SELECT $1,$3,'member.invitation_revoked',jsonb_build_object('invitation',id,'reason','member_removed','user',$2::bigint) FROM revoked`, company, id, u.ID)
		if err == nil {
			_, err = tx.Exec(ctx, `DELETE FROM memberships WHERE org_id=$1 AND user_id=$2`, company, id)
		}
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('user',$4::bigint,'old_role',$5::text,'role',$6::text))`, company, u.ID, action, id, old, role); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(204)
}

func managerError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 403, "forbidden")
	} else {
		serverError(w, err)
	}
}
