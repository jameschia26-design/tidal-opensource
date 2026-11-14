package main

import (
	"context"
	"strings"
	"testing"
)

func TestMemberInvites(t *testing.T) {
	a, _ := testApp(t)
	a.mail.sink = func(string, string, string) error { return nil }
	h := a.routes()
	owner := seedSession(t, a, "mem-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Members Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	// Owner invites a new email with names; listing shows it; revoke removes it.
	got := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"newhire@example.com","role":"member","first_name":"New","last_name":"Hire"}`, owner), 201)
	if got["status"].(string) != "invited" {
		t.Fatal(got)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/members/invitations?org_id="+o, "", owner), 200)
	rows := list["invitations"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["email"].(string) != "newhire@example.com" {
		t.Fatal(list)
	}
	id := itoa(int64(rows[0].(map[string]any)["id"].(float64)))
	if rec := do(t, h, "DELETE", "/api/v1/members/invitations/"+id+"?org_id="+o, "", owner); rec.Code != 204 {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body.String())
	}
	list = expectStatus(t, do(t, h, "GET", "/api/v1/members/invitations?org_id="+o, "", owner), 200)
	if len(list["invitations"].([]any)) != 0 {
		t.Fatal(list)
	}
	// Existing user must explicitly accept.
	teammate := seedSession(t, a, "teammate@example.com")
	if _, err := a.db.Exec(context.Background(), `UPDATE users SET email_verified_at=now() WHERE email='teammate@example.com'`); err != nil {
		t.Fatal(err)
	}
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"teammate@example.com","role":"viewer","first_name":"","last_name":""}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(int64(inv["invitation_id"].(float64)))+"/accept", "", teammate), 200)
	members := expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+o, "", owner), 200)
	if len(members["members"].([]any)) != 2 {
		t.Fatal(members)
	}
	// Only owners/admins can invite; viewers and outsiders refused.
	viewer := seedSession(t, a, "mem-viewer@example.com")
	expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"teammate@example.com","role":"viewer"}`, viewer), 403)
	// An admin cannot create another admin; the owner can.
	admin := seedSession(t, a, "mem-admin@example.com")
	if _, err := a.db.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'admin' FROM users WHERE email='mem-admin@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"newadmin@example.com","role":"admin"}`, admin), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"newadmin@example.com","role":"admin"}`, owner), 201)
}

func TestCompanyInvitationLifecycle(t *testing.T) {
	a, _ := testApp(t)
	sent := 0
	a.mail.sink = func(to, subject, body string) error {
		if !strings.Contains(body, "http://app.test/invitations") || !strings.Contains(body, "explicitly accept") {
			t.Fatalf("bad email: %s", body)
		}
		sent++
		return nil
	}
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "life-owner@example.com")
	target := seedSession(t, a, "life-target@example.com")
	stranger := seedSession(t, a, "life-stranger@example.com")
	org := orgFromList(t, a, owner, `{"name":"Lifecycle","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	invite := func(email, role string) string {
		t.Helper()
		v := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"`+email+`","role":"`+role+`"}`, owner), 201)
		return itoa(int64(v["invitation_id"].(float64)))
	}
	id := invite("life-target@example.com", "viewer")
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", target), 404)
	if _, err := a.db.Exec(ctx, `UPDATE users SET email_verified_at=now() WHERE email IN ('life-target@example.com','life-stranger@example.com')`); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", stranger), 404)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/invitations", "", target), 200)
	if len(list["invitations"].([]any)) != 1 {
		t.Fatal(list)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"life-target@example.com","role":"member"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", target), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", target), 404)
	var uid int64
	if err := a.db.QueryRow(ctx, `SELECT id FROM users WHERE email='life-target@example.com'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/members/" + itoa(uid) + "?org_id=" + o
	if rec := do(t, h, "PATCH", path, `{"role":"admin"}`, owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "DELETE", path, "", owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+o, "", target), 403)
	revoked := invite("life-target@example.com", "member")
	if rec := do(t, h, "DELETE", "/api/v1/members/invitations/"+revoked+"?org_id="+o, "", owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+revoked+"/accept", "", target), 404)
	expired := invite("life-target@example.com", "member")
	if _, err := a.db.Exec(ctx, `UPDATE invitations SET expires_at=now()-interval '1 second' WHERE id=$1`, expired); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+expired+"/accept", "", target), 404)
	if sent != 3 {
		t.Fatalf("sent %d", sent)
	}
	var count int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action IN ('member.invited','member.invitation_accepted','member.invitation_revoked','member.role_changed','member.removed')`, org).Scan(&count); err != nil || count != 7 {
		t.Fatalf("audit %d %v", count, err)
	}
}

func TestInvitationGoogleAndBooks(t *testing.T) {
	a, _ := testApp(t)
	a.mail.sink = func(string, string, string) error { return nil }
	ctx := context.Background()
	h := a.routes()
	owner := seedSession(t, a, "book-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Parent","fiscal_year_end":"12-31"}`)
	book := expectStatus(t, do(t, h, "POST", "/api/v1/organizations/"+itoa(org)+"/books", `{"name":"Child","fiscal_year_end":"12-31"}`, owner), 201)
	bid := itoa(int64(book["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+bid+`,"email":"newgoogle@example.com","role":"member"}`, owner), 201)
	id := itoa(int64(inv["invitation_id"].(float64)))
	uid, err := a.resolveGoogleUser(ctx, googleClaims{Sub: "company-google", Email: "newgoogle@example.com", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	var tier string
	var memberships int
	if err := a.db.QueryRow(ctx, `SELECT tier,(SELECT count(*) FROM memberships WHERE user_id=$1) FROM users WHERE id=$1`, uid).Scan(&tier, &memberships); err != nil || tier != "essential" || memberships != 0 {
		t.Fatalf("tier %s members %d err %v", tier, memberships, err)
	}
	token, err := a.issueSession(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", token), 200)
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+bid, "", token), 200)
	if rec := do(t, h, "DELETE", "/api/v1/members/"+itoa(uid)+"?org_id="+bid, "", owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+bid, "", token), 403)
}

func TestLegacyChildInvitationMigration(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	owner := seedSession(t, a, "migration-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Parent","fiscal_year_end":"12-31"}`)
	var uid int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email='migration-owner@example.com'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	var child int64
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(name,owner_user_id,parent_company_id) VALUES('Child',$1,$2) RETURNING id`, uid, org).Scan(&child); err != nil {
		t.Fatal(err)
	}
	// Recreate the old state before applying the new migration SQL.
	if _, err := pool.Exec(ctx, `ALTER TABLE invitations DROP COLUMN revoked_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO invitations(email,tier,org_id,role,created_by,expires_at) VALUES ('collision@example.com','growth',$1,'admin',$3,now()+interval '1 day'),('collision@example.com','growth',$2,'viewer',$3,now()+interval '1 day'),('child@example.com','growth',$2,'member',$3,now()+interval '1 day'),('old@example.com','growth',$2,'admin',$3,now()-interval '1 day'),('collision@example.com','growth',$1,'viewer',$3,now()+interval '1 day'),('migration-owner@example.com','growth',$1,'admin',$3,now()+interval '1 day')`, org, child, uid); err != nil {
		t.Fatal(err)
	}
	sql, err := migrationFiles.ReadFile("migrations/119_invitation_revocation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	var count int
	var role string
	if err = pool.QueryRow(ctx, `SELECT count(*),min(role) FROM invitations WHERE email='collision@example.com' AND org_id=$1 AND revoked_at IS NULL`, org).Scan(&count, &role); err != nil || count != 2 || role != "admin" {
		t.Fatalf("collision %d %s %v", count, role, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE email='migration-owner@example.com' AND org_id=$1 AND revoked_at IS NULL`, org).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing member invitation %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE org_id=$1 AND revoked_at IS NOT NULL`, child).Scan(&count); err != nil || count != 2 {
		t.Fatalf("active child invitations revoked %d %v", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE org_id=$1 AND revoked_at IS NULL`, child).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expired child invitations unchanged %d %v", count, err)
	}
}

func TestMemberHierarchyAndIsolation(t *testing.T) {
	a, _ := testApp(t)
	a.mail.sink = func(string, string, string) error { return nil }
	ctx := context.Background()
	h := a.routes()
	owner := seedSession(t, a, "hier-owner@example.com")
	admin := seedSession(t, a, "hier-admin@example.com")
	member := seedSession(t, a, "hier-member@example.com")
	outsider := seedSession(t, a, "hier-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Hierarchy","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	if _, err := a.db.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,CASE email WHEN 'hier-admin@example.com' THEN 'admin' ELSE 'member' END FROM users WHERE email IN ('hier-admin@example.com','hier-member@example.com')`, org); err != nil {
		t.Fatal(err)
	}
	var ownerID, adminID, memberID int64
	for email, dest := range map[string]*int64{"hier-owner@example.com": &ownerID, "hier-admin@example.com": &adminID, "hier-member@example.com": &memberID} {
		if err := a.db.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ actor, method, id, body string }{{admin, "DELETE", itoa(ownerID), ""}, {owner, "DELETE", itoa(ownerID), ""}, {admin, "DELETE", itoa(adminID), ""}, {admin, "PATCH", itoa(memberID), `{"role":"admin"}`}, {member, "DELETE", itoa(adminID), ""}, {outsider, "DELETE", itoa(memberID), ""}} {
		expectStatus(t, do(t, h, tc.method, "/api/v1/members/"+tc.id+"?org_id="+o, tc.body, tc.actor), 403)
	}
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"admin-pending@example.com","role":"admin"}`, owner), 201)
	id := itoa(int64(inv["invitation_id"].(float64)))
	expectStatus(t, do(t, h, "DELETE", "/api/v1/members/invitations/"+id+"?org_id="+o, "", admin), 403)
}

func TestMultipleCompanyInvitations(t *testing.T) {
	a, _ := testApp(t)
	a.mail.sink = func(string, string, string) error { return nil }
	h := a.routes()
	ctx := context.Background()
	owner1 := seedSession(t, a, "multi-owner1@example.com")
	owner2 := seedSession(t, a, "multi-owner2@example.com")
	target := seedSession(t, a, "multi-target@example.com")
	if _, err := a.db.Exec(ctx, `UPDATE users SET email_verified_at=now() WHERE email='multi-target@example.com'`); err != nil {
		t.Fatal(err)
	}
	org1 := orgFromList(t, a, owner1, `{"name":"One","fiscal_year_end":"12-31"}`)
	org2 := orgFromList(t, a, owner2, `{"name":"Two","fiscal_year_end":"12-31"}`)
	for _, v := range []struct {
		token string
		org   int64
	}{{owner1, org1}, {owner2, org2}} {
		expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+itoa(v.org)+`,"email":"multi-target@example.com","role":"viewer"}`, v.token), 201)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/invitations", "", target), 200)
	invites := list["invitations"].([]any)
	if len(invites) != 2 {
		t.Fatal(list)
	}
	for _, v := range invites {
		id := itoa(int64(v.(map[string]any)["id"].(float64)))
		expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", target), 200)
	}
	for _, org := range []int64{org1, org2} {
		expectStatus(t, do(t, h, "GET", "/api/v1/organizations/"+itoa(org), "", target), 200)
	}
}

func TestInvitationPasswordSignup(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	owner := seedSession(t, a, "signup-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Signup","fiscal_year_end":"12-31"}`)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+itoa(org)+`,"email":"invite-signup@example.com","role":"member"}`, owner), 201)
	id := itoa(int64(inv["invitation_id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/auth/signup", `{"email":"invite-signup@example.com","password":"correct horse battery"}`, ""), 202)
	rec := do(t, h, "POST", "/api/v1/auth/signup/verify", `{"email":"invite-signup@example.com","code":"`+sink.code(t)+`"}`, "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	token := sessionCookieValue(rec)
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+itoa(org), "", token), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+id+"/accept", "", token), 200)
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+itoa(org), "", token), 200)
}

func TestLegacyDuplicateInvitationLifecycle(t *testing.T) {
	a, _ := testApp(t)
	ctx := context.Background()
	h := a.routes()
	owner := seedSession(t, a, "legacy-owner@example.com")
	target := seedSession(t, a, "legacy-target@example.com")
	org := orgFromList(t, a, owner, `{"name":"Legacy","fiscal_year_end":"12-31"}`)
	var ownerID, targetID int64
	if err := a.db.QueryRow(ctx, `UPDATE users SET email_verified_at=now() WHERE email='legacy-owner@example.com' RETURNING id`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(ctx, `UPDATE users SET email_verified_at=now() WHERE email='legacy-target@example.com' RETURNING id`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	var viewer, admin, memberInvite int64
	for _, v := range []struct {
		email, role string
		id          *int64
	}{
		{"legacy-target@example.com", "viewer", &viewer}, {"legacy-target@example.com", "admin", &admin}, {"legacy-owner@example.com", "admin", &memberInvite},
	} {
		if err := a.db.QueryRow(ctx, `INSERT INTO invitations(email,tier,org_id,role,created_by,expires_at) VALUES($1,'essential',$2,$3,$4,now()+interval '1 day') RETURNING id`, v.email, org, v.role, ownerID).Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	o := itoa(org)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/members/invitations?org_id="+o, "", owner), 200)
	if len(list["invitations"].([]any)) != 3 {
		t.Fatal(list)
	}
	list = expectStatus(t, do(t, h, "GET", "/api/v1/invitations", "", target), 200)
	if len(list["invitations"].([]any)) != 2 {
		t.Fatal(list)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/members/invite", `{"org_id":`+o+`,"email":"legacy-target@example.com","role":"viewer"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(viewer)+"/accept", "", target), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(admin)+"/accept", "", target), 409)
	// Existing membership must never be replaced by the invitation role.
	var role string
	if err := a.db.QueryRow(ctx, `SELECT role FROM memberships WHERE org_id=$1 AND user_id=$2`, org, targetID).Scan(&role); err != nil || role != "viewer" {
		t.Fatalf("role %s: %v", role, err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(memberInvite)+"/accept", "", owner), 409)
	otherOwner := seedSession(t, a, "other-owner@example.com")
	otherOrg := orgFromList(t, a, otherOwner, `{"name":"Other","fiscal_year_end":"12-31"}`)
	var other, mixed int64
	if err := a.db.QueryRow(ctx, `INSERT INTO invitations(email,tier,org_id,role,created_by,expires_at) VALUES('legacy-target@example.com','essential',$1,'admin',$2,now()+interval '1 day') RETURNING id`, otherOrg, ownerID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(ctx, `INSERT INTO invitations(email,tier,org_id,role,created_by,expires_at) VALUES(' legacy-target@example.com ','essential',$1,'admin',$2,now()+interval '1 day') RETURNING id`, org, ownerID).Scan(&mixed); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, "DELETE", "/api/v1/members/"+itoa(targetID)+"?org_id="+o, "", owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(admin)+"/accept", "", target), 404)
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+o, "", target), 403)
	var revoked, audits int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE id=ANY($1) AND revoked_at IS NOT NULL`, []int64{admin, mixed}).Scan(&revoked); err != nil || revoked != 2 {
		t.Fatalf("revoked %d: %v", revoked, err)
	}
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='member.invitation_revoked' AND detail->>'reason'='member_removed' AND (detail->>'invitation')::bigint=ANY($2)`, org, []int64{admin, mixed}).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("audits %d: %v", audits, err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invitations/"+itoa(other)+"/accept", "", target), 200)

	var count int
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM invitations WHERE id=ANY($1)`, []int64{viewer, admin, memberInvite}).Scan(&count); err != nil || count != 3 {
		t.Fatalf("rows %d: %v", count, err)
	}
}
