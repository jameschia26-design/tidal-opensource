package main

import "testing"

func TestFixedAssetReport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "asset-report@example.com")
	outsider := seedSession(t, a, "asset-report-other@example.com")
	o := itoa(orgFromList(t, a, owner, `{"name":"Assets","fiscal_year_end":"12-31"}`))
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Machine","acquired_on":"2026-01-01","cost_cents":12000,"useful_life_months":12}`, owner), 201)
	for _, month := range []string{"2026-01", "2026-02", "2026-03"} {
		expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"`+month+`"}`, owner), 200)
	}
	path := "/api/v1/reports/fixed-assets?org_id=" + o + "&from=2026-02-01&to=2026-02-28"
	rows := expectStatus(t, do(t, h, "GET", path, "", owner), 200)["rows"].([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	row := rows[0].(map[string]any)
	if row["opening_accum_cents"] != float64(1000) || row["period_dep_cents"] != float64(1000) || row["closing_book_cents"] != float64(10000) {
		t.Fatal(row)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Future machine","acquired_on":"2026-04-15","cost_cents":12000,"useful_life_months":12}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-04"}`, owner), 200)
	future := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-assets?org_id="+o+"&from=2026-04-10&to=2026-04-30", "", owner), 200)["rows"].([]any)
	if len(future) != 2 || future[0].(map[string]any)["opening_book_cents"] != float64(-1000) {
		t.Fatal(future)
	}
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-assets?org_id="+o+"&from=bad&to=2026-02-28", "", owner), 400)
	// Depreciation must not post for months before acquisition.
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Later machine","acquired_on":"2026-06-10","cost_cents":1200,"useful_life_months":12}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-05"}`, owner), 200)
	later := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-assets?org_id="+o+"&from=2026-05-01&to=2026-07-31", "", owner), 200)["rows"].([]any)
	for _, r := range later {
		if r.(map[string]any)["name"] == "Later machine" && r.(map[string]any)["closing_accum_cents"] != float64(0) {
			t.Fatal("early depreciation posted", r)
		}
	}
}
