package main

import (
	"context"
	"testing"
)

func TestFixedAssetMethodsResidualImportAndDisposalSchedule(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "asset-methods@example.com")
	org := orgFromList(t, a, owner, `{"name":"Asset methods","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	create := func(body string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", body, owner), 201)["id"].(float64))
	}
	dim := create(`{"org_id":` + o + `,"name":"Diminishing","acquired_on":"2026-01-01","cost_cents":12000,"useful_life_months":12,"depreciation_method":"diminishing_value","averaging_rule":"full_month","residual_value_cents":2000}`)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-01"}`, owner), 200)
	var charge int64
	if err := pool.QueryRow(ctx, `SELECT amount_cents FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2`, org, dim).Scan(&charge); err != nil {
		t.Fatal(err)
	}
	if charge != 2000 {
		t.Fatalf("diminishing charge=%d, want 2000", charge)
	}
	// Monthly diminishing charges are taken from the opening carrying value, so the second month is
	// smaller than the first. A fixed charge from original cost would repeat 2000.
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-02"}`, owner), 200)
	if err := pool.QueryRow(ctx, `SELECT amount_cents FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2 AND period_month='2026-02-01'`, org, dim).Scan(&charge); err != nil {
		t.Fatal(err)
	}
	if charge != 1667 {
		t.Fatalf("second diminishing charge=%d, want 1667", charge)
	}
	manual := create(`{"org_id":` + o + `,"name":"Manual","acquired_on":"2026-01-01","cost_cents":10000,"useful_life_months":12,"depreciation_method":"manual","residual_value_cents":1000}`)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-01","manual_amounts":[{"asset_id":`+itoa(manual)+`,"amount_cents":1500}]}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/import", `{"org_id":`+o+`,"csv":"name,acquired_on,cost_cents,useful_life_months,depreciation_method,averaging_rule,residual_value_cents\\nImported,2026-02-01,5000,10,straight_line,half_month,500"}`, owner), 201)
	accounts := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+o, "", owner), 200)["accounts"].([]any)
	var accountID int64
	for _, x := range accounts {
		v := x.(map[string]any)
		if v["type"] == "asset" {
			accountID = int64(v["id"].(float64))
			break
		}
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/"+itoa(dim)+"/dispose", `{"org_id":`+o+`,"disposed_on":"2026-02-01","proceeds_cents":9000,"proceeds_account_id":`+itoa(accountID)+`}`, owner), 200)
	schedule := expectStatus(t, do(t, h, "GET", "/api/v1/reports/disposal-schedule?org_id="+o, "", owner), 200)["rows"].([]any)
	if len(schedule) != 1 || schedule[0].(map[string]any)["id"] != float64(dim) {
		t.Fatalf("disposal schedule=%v", schedule)
	}
}

func TestFixedAssetsDepreciateStraightLineOnce(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "asset-owner@example.com")
	viewer := seedSession(t, a, "asset-viewer@example.com")
	outsider := seedSession(t, a, "asset-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Asset co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='asset-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	// Cost 1000.00 over 3 months: 333.33, 333.33, 333.34 (remainder on the last month so totals match cost).
	body := `{"org_id":` + itoa(org) + `,"name":"Laptop","acquired_on":"2026-01-01","cost_cents":100000,"useful_life_months":3}`
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", body, viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", body, outsider), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+itoa(org)+`,"name":"Bad","acquired_on":"2026-01-01","cost_cents":0,"useful_life_months":3}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", body, owner), 201)

	run := func(month string, token string) map[string]any {
		return expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+itoa(org)+`,"month":"`+month+`"}`, token), 200)
	}
	if got := run("2026-01", owner)["assets_depreciated"].([]any); len(got) != 1 {
		t.Fatal("first run", got)
	}
	// Repeating the same month must not post again.
	if got := run("2026-01", owner)["assets_depreciated"].([]any); len(got) != 0 {
		t.Fatal("duplicate depreciation in same month", got)
	}
	run("2026-02", owner)
	run("2026-03", owner)
	// Asset fully depreciated: further runs post nothing.
	if got := run("2026-04", owner)["assets_depreciated"].([]any); len(got) != 0 {
		t.Fatal("depreciated past life", got)
	}
	var total int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM asset_depreciation WHERE org_id=$1`, org).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 100000 {
		t.Fatal("depreciation does not equal cost", total)
	}
	var last int64
	if err := pool.QueryRow(ctx, `SELECT amount_cents FROM asset_depreciation WHERE org_id=$1 AND period_month='2026-03-01'`, org).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if last != 33334 {
		t.Fatal("final month remainder wrong", last)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/assets?org_id="+itoa(org), "", viewer), 200)["assets"].([]any)
	if listed[0].(map[string]any)["net_book_cents"] != float64(0) {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/assets?org_id="+itoa(org), "", outsider), 403)
	if _, err := pool.Exec(ctx, `UPDATE asset_depreciation SET amount_cents=1 WHERE org_id=$1`, org); err == nil {
		t.Fatal("depreciation mutable")
	}
}
