package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// TestMemberPostingPermission is the Phase 5 permission contract: a company manager can take document
// posting away from a member, the backend refuses posting for that member until it is given back, and
// the change is recorded in the audit trail.
func TestMemberPostingPermission(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "perm-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Permission co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	member := seedSession(t, a, "perm-member@example.com")
	var memberID int64
	if err := a.db.QueryRow(context.Background(), `SELECT id FROM users WHERE email='perm-member@example.com'`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) VALUES($1,$2,'member')`, org, memberID); err != nil {
		t.Fatal(err)
	}
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Perm customer"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":1000}]}`, owner), 201)
	invID := itoa(int64(inv["id"].(float64)))

	// A member may post by default.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+invID+"/post?org_id="+o, "", member), 200)

	second := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-02","due_date":"2026-03-31","lines":[{"description":"More","quantity":1,"unit_price_cents":1000}]}`, owner), 201)
	secondID := itoa(int64(second["id"].(float64)))
	expectStatus(t, do(t, h, "PATCH", "/api/v1/members/"+itoa(memberID)+"/permissions", `{"org_id":`+o+`,"post_documents":false}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+secondID+"/post?org_id="+o, "", member), 403)

	var events int
	if err := a.db.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='member.permissions_changed'`, org).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("permission change recorded %d times, want 1", events)
	}

	expectStatus(t, do(t, h, "PATCH", "/api/v1/members/"+itoa(memberID)+"/permissions", `{"org_id":`+o+`,"post_documents":true}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+secondID+"/post?org_id="+o, "", member), 200)

	// The owner is not subject to the override, and a viewer cannot be granted posting by it.
	var ownerID int64
	if err := a.db.QueryRow(context.Background(), `SELECT id FROM users WHERE email='perm-owner@example.com'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "PATCH", "/api/v1/members/"+itoa(ownerID)+"/permissions", `{"org_id":`+o+`,"post_documents":false}`, owner), 403)
}

// TestTimerBecomesChargeableTimeAndReports covers the timer and chargeable-time contract: stopping a
// running timer writes a normal time entry with its billable flag and rate, and the project report
// shows both the total and the chargeable subset.
func TestTimerBecomesChargeableTimeAndReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "timer-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Timer co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Timer customer"}`, owner), 201)["id"].(float64)))
	project := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Timer project"}`, owner), 201)["id"].(float64)))

	if got := expectStatus(t, do(t, h, "GET", "/api/v1/time/timer?org_id="+o, "", owner), 200); got["timer"] != nil {
		t.Fatalf("timer should start empty: %v", got)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/time/timer/start", `{"org_id":`+o+`,"project_id":`+project+`,"task":"Build","description":"On site","billable":true,"rate_cents":12000}`, owner), 201)
	// Move the start back 90 minutes so the stop produces a known duration instead of one minute.
	if _, err := a.db.Exec(context.Background(), `UPDATE time_timers SET started_at = now() - interval '90 minutes' WHERE user_id=(SELECT id FROM users WHERE email='timer-owner@example.com')`); err != nil {
		t.Fatal(err)
	}
	stopped := expectStatus(t, do(t, h, "POST", "/api/v1/time/timer/stop", `{"org_id":`+o+`}`, owner), 200)
	if minutes := int(stopped["minutes"].(float64)); minutes != 90 {
		t.Fatalf("timer stopped at %d minutes, want 90", minutes)
	}
	if got := expectStatus(t, do(t, h, "GET", "/api/v1/time/timer?org_id="+o, "", owner), 200); got["timer"] != nil {
		t.Fatalf("timer still running after stop: %v", got)
	}

	days := "from=2026-01-01&to=2026-12-31"
	rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-time?org_id="+o+"&status=all&"+days, "", owner), 200)["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("project report rows %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["minutes"].(float64) != 90 || row["billable_minutes"].(float64) != 90 || row["chargeable_value_cents"].(float64) != 18000 {
		t.Fatalf("chargeable project totals %v", row)
	}
	// A non-billable entry counts in minutes but not in chargeable time.
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", `{"org_id":`+o+`,"project_id":`+project+`,"date":"2026-03-02","task":"Admin","minutes":30,"billable":false,"rate_cents":12000}`, owner), 201)
	row = expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-time?org_id="+o+"&status=all&"+days, "", owner), 200)["rows"].([]any)[0].(map[string]any)
	if row["minutes"].(float64) != 120 || row["billable_minutes"].(float64) != 90 || row["chargeable_value_cents"].(float64) != 18000 {
		t.Fatalf("non-billable entry leaked into chargeable totals: %v", row)
	}
}

// TestGlobalSearchAndLoginHistory covers the two remaining Phase 5 controls: header search reaches
// each record type, and the signed-in user can see their own recent sign-ins.
func TestGlobalSearchAndLoginHistory(t *testing.T) {
	a, _ := testApp(t)
	sink := mailSink(a)
	h := a.routes()
	const email, password = "search-history@example.com", "correct horse battery"
	mustVerify(t, a, email, password)
	owner := seedSession(t, a, "search-owner2@example.com")
	org := orgFromList(t, a, owner, `{"name":"Search co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Sunrise Bakery"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Sunrise loaf","item_type":"goods","sku":"SL-1","selling_price_cents":500,"cost_price_cents":200,"sales_account_id":0,"purchase_account_id":0}`, owner), 201)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/search?org_id="+o+"&q=sunrise", "", owner), 200)
	if len(got["contacts"].([]any)) != 1 || len(got["items"].([]any)) != 1 {
		t.Fatalf("search missed a record type: %v", got)
	}
	short := expectStatus(t, do(t, h, "GET", "/api/v1/search?org_id="+o+"&q=s", "", owner), 200)
	if len(short["contacts"].([]any)) != 0 {
		t.Fatalf("one-character search must stay empty: %v", short)
	}

	login := do(t, h, http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password), "")
	if login.Code != http.StatusAccepted {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/auth/login/verify", fmt.Sprintf(`{"email":%q,"code":%q}`, email, sink.code(t)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login verify: %d %s", rec.Code, rec.Body)
	}
	cookie := sessionCookieValue(rec)
	hist := expectStatus(t, do(t, h, "GET", "/api/v1/login-history", "", cookie), 200)["events"].([]any)
	if len(hist) == 0 {
		t.Fatal("login history is empty after a successful sign-in")
	}
}
