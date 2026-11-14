package main

import "testing"

func TestStaffRates(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "rate-owner@example.com")
	viewer := seedSession(t, a, "rate-viewer@example.com")
	outsider := seedSession(t, a, "rate-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Rates co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='rate-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE users SET name='Rate Owner' WHERE email='rate-owner@example.com'`); err != nil {
		t.Fatal(err)
	}
	list := func(tok string) []any {
		return expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates?org_id="+itoa(org), "", tok), 200)["staff"].([]any)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates?org_id="+itoa(org), "", viewer), 403)
	staff := list(owner)
	if len(staff) != 2 || staff[0].(map[string]any)["hourly_cents"].(float64) != 0 {
		t.Fatal(staff)
	}
	var ownerID, viewerID int64
	for _, s := range staff {
		m := s.(map[string]any)
		if m["email"] == "rate-owner@example.com" {
			if m["name"] != "Rate Owner" {
				t.Fatal(m)
			}
			ownerID = int64(m["user_id"].(float64))
		} else {
			viewerID = int64(m["user_id"].(float64))
		}
	}
	set := func(uid int64, cents string, tok string) int {
		return do(t, h, "POST", "/api/v1/staff-rates", `{"org_id":`+itoa(org)+`,"user_id":`+itoa(uid)+`,"hourly_cents":`+cents+`}`, tok).Code
	}
	if c := set(viewerID, "5000", viewer); c != 403 {
		t.Fatal("viewer set rate", c)
	}
	if c := set(viewerID, "-1", owner); c != 400 {
		t.Fatal("negative accepted", c)
	}
	if c := set(999999, "100", owner); c != 422 {
		t.Fatal("non-member accepted", c)
	}
	if c := set(ownerID, "4500", owner); c != 200 {
		t.Fatal(c)
	}
	if c := set(ownerID, "5000", owner); c != 200 {
		t.Fatal(c)
	}
	for _, s := range list(owner) {
		m := s.(map[string]any)
		want := 0.0
		if int64(m["user_id"].(float64)) == ownerID {
			want = 5000
		}
		if m["hourly_cents"].(float64) != want {
			t.Fatal(m)
		}
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates?org_id="+itoa(org), "", outsider), 403)
}

func TestStaffRateHistory(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ratehist-owner@example.com")
	viewer := seedSession(t, a, "ratehist-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"RateHist co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='ratehist-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	var ownerID int64
	for _, s := range expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates?org_id="+itoa(org), "", owner), 200)["staff"].([]any) {
		m := s.(map[string]any)
		if m["email"] == "ratehist-owner@example.com" {
			ownerID = int64(m["user_id"].(float64))
			if m["change_count"].(float64) != 0 {
				t.Fatal("history before any save", m)
			}
		}
	}
	history := func(tok string) []any {
		return expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates/"+itoa(ownerID)+"/history?org_id="+itoa(org), "", tok), 200)["history"].([]any)
	}
	if c := do(t, h, "GET", "/api/v1/staff-rates/"+itoa(ownerID)+"/history?org_id="+itoa(org), "", viewer).Code; c != 403 {
		t.Fatal("viewer history", c)
	}
	set := func(cents int) {
		expectStatus(t, do(t, h, "POST", "/api/v1/staff-rates", `{"org_id":`+itoa(org)+`,"user_id":`+itoa(ownerID)+`,"hourly_cents":`+itoa(int64(cents))+`}`, owner), 200)
	}
	set(4000)
	set(4000) // same value again: no new history row
	if n := len(history(owner)); n != 1 {
		t.Fatalf("history after duplicate save = %d, want 1", n)
	}
	set(5000)
	rows := history(owner)
	if len(rows) != 2 || rows[0].(map[string]any)["hourly_cents"].(float64) != 5000 || rows[0].(map[string]any)["changed_by"] != "ratehist-owner@example.com" {
		t.Fatal(rows)
	}
	for _, s := range expectStatus(t, do(t, h, "GET", "/api/v1/staff-rates?org_id="+itoa(org), "", owner), 200)["staff"].([]any) {
		m := s.(map[string]any)
		if m["email"] == "ratehist-owner@example.com" && (m["change_count"].(float64) != 2 || m["last_changed_at"] == nil) {
			t.Fatal(m)
		}
	}
}
