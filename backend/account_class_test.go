package main

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// migrationsThrough builds the migration set up to and including max, so a test can insert rows into
// the old shape and then run the real upgrade on top.
func migrationsThrough(t *testing.T, max string) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name()[:3] > max {
			continue
		}
		body, err := fs.ReadFile(migrationFiles, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		files["migrations/"+e.Name()] = &fstest.MapFile{Data: body}
	}
	return files
}

// TestAccountClassBackfill runs migration 120 against a chart written in the old 5-type shape and
// checks every documented rule: each broad type and each code-prefix branch.
func TestAccountClassBackfill(t *testing.T) {
	_, pool := testApp(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	if err := migrateFS(ctx, pool, migrationsThrough(t, "119")); err != nil {
		t.Fatal(err)
	}
	var user, org int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('backfill@example.com','x') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, owner_user_id) VALUES ('Backfill Pte', $1) RETURNING id`, user).Scan(&org); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ code, name, typ, want string }{
		{"4000", "Sales", "revenue", "sales"},
		{"4500", "Consulting", "revenue", "sales"},
		{"8000", "Other trading", "revenue", "revenue"},
		{"5100", "Cost of Sales", "expense", "cost_of_sales"},
		{"5900", "Direct costs", "expense", "cost_of_sales"},
		{"6100", "Rent", "expense", "expense"},
		{"1000", "Bank", "asset", "current_asset"},
		{"2800", "Savings", "asset", "bank"},
		{"1500", "Fixed assets", "asset", "fixed_asset"},
		{"1700", "Equipment", "asset", "fixed_asset"},
		{"2400", "Stock", "asset", "inventory"},
		{"1300", "Inventory", "asset", "current_asset"},
		{"2100", "Accounts Payable", "liability", "current_liability"},
		{"2300", "GST Output", "liability", "current_liability"},
		{"3900", "Opening Balance Equity", "equity", "equity"},
		{"3901", "Retained earnings", "equity", "retained_earnings"},
		{"3902", "Accumulated Profit", "equity", "retained_earnings"},
	}
	for _, r := range rows {
		if _, err := pool.Exec(ctx, `INSERT INTO accounts (org_id, code, name, type) VALUES ($1,$2,$3,$4)`, org, r.code, r.name, r.typ); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateFS(ctx, pool, migrationFiles); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	for _, r := range rows {
		var got string
		if err := pool.QueryRow(ctx, `SELECT class FROM accounts WHERE org_id=$1 AND code=$2`, org, r.code).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != r.want {
			t.Errorf("account %s (%s, %s): class = %q, want %q", r.code, r.name, r.typ, got, r.want)
		}
	}
	// The group constraint is part of the migration: a foreign class cannot be stored.
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (org_id, code, name, type, class) VALUES ($1,'9999','Wrong','asset','sales')`, org); err == nil {
		t.Fatal("class outside its type group was accepted")
	}
}

func TestDefaultAccountClass(t *testing.T) {
	for _, c := range []struct{ typ, code, want string }{
		{"revenue", "4000", "sales"},
		{"revenue", "9000", "revenue"},
		{"expense", "5100", "cost_of_sales"},
		{"expense", "6100", "expense"},
		{"asset", "2800", "bank"},
		{"asset", "1500", "fixed_asset"},
		{"asset", "1799", "fixed_asset"},
		{"asset", "2400", "inventory"},
		{"asset", "1000", "current_asset"},
		{"liability", "2300", "current_liability"},
		{"equity", "3900", "equity"},
	} {
		if got := defaultAccountClass(c.typ, c.code); got != c.want {
			t.Errorf("defaultAccountClass(%q, %q) = %q, want %q", c.typ, c.code, got, c.want)
		}
	}
	// Every class in the catalogue belongs to exactly one broad group.
	if len(accountClassGroups) != 16 {
		t.Fatalf("class catalogue has %d entries, want 16", len(accountClassGroups))
	}
	for class, group := range accountClassGroups {
		if classGroup(class) != group || group == "" {
			t.Errorf("%s: group = %q", class, group)
		}
	}
}

func TestAccountClassAPI(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "account-class@example.com")
	org := orgFromList(t, a, owner, `{"name":"Account class company","fiscal_year_end":"12-31"}`)
	post := func(body string) int {
		return do(t, h, "POST", "/api/v1/accounts", body, owner).Code
	}
	if code := post(`{"org_id":` + itoa(org) + `,"code":"1200","name":"Unknown","type":"asset","class":"banking"}`); code != 400 {
		t.Fatalf("unknown class returned %d, want 400", code)
	}
	if code := post(`{"org_id":` + itoa(org) + `,"code":"1210","name":"Mismatch","type":"asset","class":"sales"}`); code != 400 {
		t.Fatalf("class outside its type group returned %d, want 400", code)
	}
	for _, c := range []struct{ code, name, typ, class string }{
		{"2810", "Savings", "asset", "bank"},
		{"5110", "Freight", "expense", "cost_of_sales"},
		{"7110", "Other income", "revenue", "other_income"},
	} {
		created := expectStatus(t, do(t, h, "POST", "/api/v1/accounts",
			`{"org_id":`+itoa(org)+`,"code":"`+c.code+`","name":"`+c.name+`","type":"`+c.typ+`","class":"`+c.class+`"}`, owner), 201)
		if created["class"] != c.class {
			t.Fatalf("explicit class not kept: %v", created)
		}
	}
	// Omitted class is derived from the type and code, the same rules as the backfill.
	derived := expectStatus(t, do(t, h, "POST", "/api/v1/accounts",
		`{"org_id":`+itoa(org)+`,"code":"2405","name":"Stock","type":"asset"}`, owner), 201)
	if derived["class"] != "inventory" {
		t.Fatalf("derived class = %v, want inventory", derived["class"])
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+itoa(org), "", owner), 200)
	found := false
	for _, entry := range listed["accounts"].([]any) {
		account := entry.(map[string]any)
		if account["code"] == "2405" {
			found = account["class"] == "inventory"
		}
	}
	if !found {
		t.Fatal("class missing from account list", listed)
	}
}
