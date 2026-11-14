package main

import (
	"context"
	"strings"
	"testing"
)

func TestProfileUpdate(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	user := seedSession(t, a, "profile@example.com")
	other := seedSession(t, a, "profile-other@example.com")
	expectStatus(t, do(t, h, "PATCH", "/api/v1/me", `{"name":"X"}`, ""), 401)
	for _, body := range []string{`{}`, `{"name":null}`, `{"name":"  "}`, `{"name":"a\nB"}`, `{"name":"` + strings.Repeat("a", 101) + `"}`, `{"name":"X","email":"changed@example.com"}`, `{"name":"X","id":2}`} {
		expectStatus(t, do(t, h, "PATCH", "/api/v1/me", body, user), 400)
	}
	expectStatus(t, do(t, h, "PATCH", "/api/v1/me", `{"name":"  New Name  "}`, user), 200)
	me := expectStatus(t, do(t, h, "GET", "/api/v1/me", "", user), 200)
	if me["name"] != "New Name" || me["email"] != "profile@example.com" {
		t.Fatal(me)
	}
	otherMe := expectStatus(t, do(t, h, "GET", "/api/v1/me", "", other), 200)
	if otherMe["name"] == "New Name" {
		t.Fatal(otherMe)
	}
	expectStatus(t, do(t, h, "PATCH", "/api/v1/me", `{"name":"New Name"}`, user), 200)
	var count int
	if err := a.db.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='user.profile_updated' AND user_id=(SELECT id FROM users WHERE email='profile@example.com')`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count %d: %v", count, err)
	}
}
