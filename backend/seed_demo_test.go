package main

// Needs TEST_DATABASE_URL pointing at a disposable database (the schema is wiped). Skips otherwise.
// Runs the operator command twice: the first run creates the demo company, the second must be a no-op.

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSeedDemo2025CreatesOnceAndStaysIsolated(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	cfg := config{
		PublicOrigin:  "http://app.test",
		AdminEmail:    "seed-admin@example.com",
		AdminPassword: "demo-seed-password-2025",
	}
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	a.cfg.AdminEmail, a.cfg.AdminPassword = cfg.AdminEmail, cfg.AdminPassword

	var first bytes.Buffer
	if err := seedDemo2025(ctx, a, &first); err != nil {
		t.Fatalf("first run: %v\n%s", err, first.String())
	}
	for _, want := range []string{"org: ", "sales invoices posted: 12", "expenses: 12", "skipped: none"} {
		if !strings.Contains(first.String(), want) {
			t.Fatalf("first run output missing %q:\n%s", want, first.String())
		}
	}

	var orgID, orgs, invoices int64
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE name=$1`, demoOrgName).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&orgs)
	pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id=$1`, orgID).Scan(&invoices)
	if orgs != 1 || invoices == 0 {
		t.Fatalf("after first run: organizations=%d invoices=%d", orgs, invoices)
	}

	var markers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM demo_seed_runs WHERE org_id=$1 AND label=$2`, orgID, demoSeedLabel).Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if markers != 1 {
		t.Fatalf("completed run left %d completion markers, want 1", markers)
	}

	// Second run must change nothing.
	var second bytes.Buffer
	if err := seedDemo2025(ctx, a, &second); err != nil {
		t.Fatalf("second run: %v\n%s", err, second.String())
	}
	if !strings.Contains(second.String(), "already exists, nothing changed") {
		t.Fatalf("second run did not short-circuit:\n%s", second.String())
	}
	var orgsAfter, invoicesAfter int64
	pool.QueryRow(ctx, `SELECT count(*) FROM organizations`).Scan(&orgsAfter)
	pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id=$1`, orgID).Scan(&invoicesAfter)
	if orgsAfter != orgs || invoicesAfter != invoices {
		t.Fatalf("second run changed data: organizations %d->%d, invoices %d->%d", orgs, orgsAfter, invoices, invoicesAfter)
	}

	// The admin owns and lists it.
	var adminID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	adminToken, err := a.issueSession(ctx, adminID)
	if err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	list := expectStatus(t, do(t, h, http.MethodGet, "/api/v1/organizations", "", adminToken), http.StatusOK)
	listed := false
	for _, row := range list["organizations"].([]any) {
		o := row.(map[string]any)
		if int64(o["id"].(float64)) == orgID && o["name"] == demoOrgName {
			listed = true
		}
	}
	if !listed {
		t.Fatal("admin's organization list does not include the demo company")
	}

	// Another user neither sees it in the list nor reads its invoices.
	other := seedSession(t, a, "seed-other@example.com")
	otherList := expectStatus(t, do(t, h, http.MethodGet, "/api/v1/organizations", "", other), http.StatusOK)
	if strings.Contains(otherListBody(otherList), demoOrgName) {
		t.Fatal("another user can see the demo company")
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/invoices?org_id="+coreID(orgID), "", other); rec.Code != http.StatusForbidden {
		t.Fatalf("other user read demo invoices: %d", rec.Code)
	}
}

// An existing company without a completed-run marker refuses the no-change shortcut: a partial
// demo run must fail loudly instead of being reported as up to date.
func TestSeedDemo2025RejectsUnmarkedCompany(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	cfg := config{
		PublicOrigin:  "http://app.test",
		AdminEmail:    "seed-admin@example.com",
		AdminPassword: "demo-seed-password-2025",
	}
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		t.Fatal(err)
	}
	a.cfg.AdminEmail, a.cfg.AdminPassword = cfg.AdminEmail, cfg.AdminPassword
	var adminID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, cfg.AdminEmail).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	var orgID int64
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, owner_user_id) VALUES ($1,$2) RETURNING id`, demoOrgName, adminID).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := seedDemo2025(ctx, a, &out); err == nil || !strings.Contains(err.Error(), "without a completed seed marker") {
		t.Fatalf("partial demo company accepted: %v\n%s", err, out.String())
	}
}

func otherListBody(m map[string]any) string {
	var b strings.Builder
	for _, row := range m["organizations"].([]any) {
		if name, ok := row.(map[string]any)["name"].(string); ok {
			b.WriteString(name)
		}
	}
	return b.String()
}
