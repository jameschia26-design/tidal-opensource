package main

import (
	"strconv"

	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"testing"
)

func TestSharedSampleAccessAndQuota(t *testing.T) {
	a, db := testApp(t)
	ctx := context.Background()
	owner := seedSession(t, a, "sample-owner@example.com")
	var ownerID int64
	if err := db.QueryRow(ctx, `SELECT id FROM users WHERE email='sample-owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	var sampleID int64
	if err := db.QueryRow(ctx, `INSERT INTO organizations(name,owner_user_id,is_shared_sample) VALUES('Example', $1, true) RETURNING id`, ownerID).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'owner')`, sampleID, ownerID); err != nil {
		t.Fatal(err)
	}
	future := seedSession(t, a, "future@example.com")
	var futureID int64
	if err := db.QueryRow(ctx, `SELECT id FROM users WHERE email='future@example.com'`).Scan(&futureID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ownerID, futureID} {
		access, _, code, err := a.orgAccessFor(ctx, id, sampleID)
		if err != nil || code != "" || access.Role != "viewer" {
			t.Fatalf("sample access: %+v %q %v", access, code, err)
		}
	}
	h := a.routes()
	for _, tok := range []string{owner, future} {
		got := do(t, h, http.MethodGet, "/api/v1/organizations", "", tok)
		if got.Code != http.StatusOK {
			t.Fatalf("list: %d %s", got.Code, got.Body.String())
		}
		got = do(t, h, http.MethodPost, "/api/v1/organizations", `{"name":"My company","fiscal_year_end":"12-31"}`, tok)
		if got.Code != http.StatusCreated {
			t.Fatalf("first company: %d %s", got.Code, got.Body.String())
		}
		got = do(t, h, http.MethodPost, "/api/v1/organizations", `{"name":"Another","fiscal_year_end":"12-31"}`, tok)
		if got.Code != http.StatusConflict {
			t.Fatalf("quota: %d %s", got.Code, got.Body.String())
		}
	}
}

func sampleFixture(t *testing.T) (a *app, db *pgxpool.Pool, sampleID int64) {
	a, db = testApp(t)
	var owner int64
	if err := db.QueryRow(context.Background(), `INSERT INTO users(email,password_hash) VALUES('op@example.com','x') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `INSERT INTO organizations(name,owner_user_id,is_shared_sample) VALUES('Example',$1,true) RETURNING id`, owner).Scan(&sampleID); err != nil {
		t.Fatal(err)
	}
	return
}

func userID(t *testing.T, db *pgxpool.Pool, email string) int64 {
	var id int64
	if err := db.QueryRow(context.Background(), `SELECT id FROM users WHERE email=$1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A real membership wins implicit selection, even when the user also has a sample membership; explicit id still reaches the sample.
func TestImplicitOrgPrefersRealMembership(t *testing.T) {
	a, db, sampleID := sampleFixture(t)
	ctx := context.Background()
	tok := seedSession(t, a, "u@example.com")
	uid := userID(t, db, "u@example.com")
	if _, err := db.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'owner')`, sampleID, uid); err != nil {
		t.Fatal(err)
	}
	if acc, _, code, err := a.orgAccessFor(ctx, uid, 0); err != nil || code != "" || acc.ID != sampleID || acc.Role != "viewer" {
		t.Fatalf("sample-only implicit: %+v %q %v", acc, code, err)
	}
	if got := do(t, a.routes(), http.MethodPost, "/api/v1/organizations", `{"name":"Real","fiscal_year_end":"12-31"}`, tok); got.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", got.Code, got.Body.String())
	}
	var realID int64
	if err := db.QueryRow(ctx, `SELECT id FROM organizations WHERE name='Real'`).Scan(&realID); err != nil {
		t.Fatal(err)
	}
	acc, _, code, err := a.orgAccessFor(ctx, uid, 0)
	if err != nil || code != "" || acc.ID != realID || acc.Role != "owner" {
		t.Fatalf("implicit should pick real company: %+v %q %v", acc, code, err)
	}
	if acc, _, code, _ = a.orgAccessFor(ctx, uid, sampleID); code != "" || acc.ID != sampleID || acc.Role != "viewer" {
		t.Fatalf("explicit sample: %+v %q", acc, code)
	}
	// Two real companies still need an explicit choice.
	if _, err := db.Exec(ctx, `INSERT INTO organizations(name,owner_user_id) VALUES('Real2',$1)`, uid); err != nil {
		t.Fatal(err)
	}
	var r2 int64
	_ = db.QueryRow(ctx, `SELECT id FROM organizations WHERE name='Real2'`).Scan(&r2)
	if _, err := db.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member')`, r2, uid); err != nil {
		t.Fatal(err)
	}
	if _, _, code, _ = a.orgAccessFor(ctx, uid, 0); code != "org_required" {
		t.Fatalf("want org_required, got %q", code)
	}
}

// Sample is read-only for everyone, including its seeded owner and users with no membership.
func TestSharedSampleIsReadOnly(t *testing.T) {
	a, db, sampleID := sampleFixture(t)
	ctx := context.Background()
	h := a.routes()
	opID := userID(t, db, "op@example.com")
	if _, err := db.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'owner')`, sampleID, opID); err != nil {
		t.Fatal(err)
	}
	var opTok string
	{
		opTok, _ = randomString(32)
		if _, err := db.Exec(ctx, `INSERT INTO sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '1 hour')`, tokenHash(opTok), opID); err != nil {
			t.Fatal(err)
		}
	}
	stranger := seedSession(t, a, "stranger@example.com")
	id := strconv.FormatInt(sampleID, 10)
	for _, tok := range []string{opTok, stranger} {
		for _, c := range []struct{ method, path, body string }{
			{http.MethodPost, "/api/v1/contacts", `{"org_id":` + id + `,"name":"X","role":"customer"}`},
			{http.MethodPut, "/api/v1/organizations/" + id, `{"name":"Hacked","fiscal_year_end":"12-31"}`},
			{http.MethodPost, "/api/v1/organizations/" + id + "/books", `{"name":"Book","fiscal_year_end":"12-31"}`},
			{http.MethodGet, "/api/v1/members?org_id=" + id, ""},
		} {
			if got := do(t, h, c.method, c.path, c.body, tok); got.Code != http.StatusForbidden {
				t.Fatalf("%s %s: want 403 got %d %s", c.method, c.path, got.Code, got.Body.String())
			}
		}
		if got := do(t, h, http.MethodGet, "/api/v1/organizations/"+id, "", tok); got.Code != http.StatusOK {
			t.Fatalf("read sample: %d", got.Code)
		}
	}
	var name string
	_ = db.QueryRow(ctx, `SELECT name FROM organizations WHERE id=$1`, sampleID).Scan(&name)
	if name != "Example" {
		t.Fatalf("sample was modified: %q", name)
	}
}

// Only one sample may exist; a second marked company is refused by the database.
func TestOnlyOneSharedSample(t *testing.T) {
	_, db, _ := sampleFixture(t)
	if _, err := db.Exec(context.Background(), `INSERT INTO organizations(name,owner_user_id,is_shared_sample) VALUES('Second',(SELECT id FROM users LIMIT 1),true)`); err == nil {
		t.Fatal("second shared sample accepted")
	}
}
