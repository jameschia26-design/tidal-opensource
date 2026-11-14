package main

import "testing"

// TestDepreciationFollowsSchedule covers B9 and B10: a late run posts every missed month in its own period
// (D8), runs out of order reach the same totals, and a tiny asset neither posts a zero journal nor stops
// the rest of the batch.
func TestDepreciationFollowsSchedule(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "dep-schedule@example.com")
	org := orgFromList(t, a, owner, `{"name":"Dep schedule","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	asset := func(name string, cents int64, life int, acquired string) int64 {
		t.Helper()
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"`+name+`","acquired_on":"`+acquired+`","cost_cents":`+itoa(cents)+`,"useful_life_months":`+itoa(int64(life))+`}`, owner), 201)["id"].(float64))
	}
	run := func(month string, want int) {
		t.Helper()
		expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"`+month+`"}`, owner), want)
	}
	months := func(id int64) (count, total int64) {
		t.Helper()
		if err := pool.QueryRow(t.Context(), `SELECT count(*), COALESCE(sum(amount_cents),0) FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2`, org, id).Scan(&count, &total); err != nil {
			t.Fatal(err)
		}
		return
	}

	// A one-cent asset and a normal asset acquired in January; one December run catches up both.
	tiny := asset("Tiny", 1, 12, "2026-01-02")
	big := asset("Machine", 1200, 12, "2026-01-02")
	run("2026-12", 200)
	if c, total := months(tiny); c != 1 || total != 1 {
		t.Fatalf("tiny asset months=%d total=%d, want 1 month of 1 cent", c, total)
	}
	if c, total := months(big); c != 12 || total != 1200 {
		t.Fatalf("big asset months=%d total=%d, want 12 months totalling 1200", c, total)
	}
	var zero int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1 AND id NOT IN (SELECT journal_id FROM journal_lines WHERE org_id=$1)`, org).Scan(&zero); err != nil {
		t.Fatal(err)
	}
	if zero != 0 {
		t.Fatalf("%d journals have no lines", zero)
	}

	// Out of order equals in order: June first, then March adds nothing; the in-order asset does the same.
	outOfOrder := asset("Out of order", 1200, 12, "2026-01-02")
	run("2026-06", 200)
	run("2026-03", 200)
	inOrder := asset("In order", 1200, 12, "2026-01-02")
	for _, month := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06"} {
		run(month, 200)
	}
	oc, ot := months(outOfOrder)
	ic, it := months(inOrder)
	if oc != ic || ot != it {
		t.Fatalf("out of order months=%d total=%d, in order months=%d total=%d", oc, ot, ic, it)
	}
	if ic != 6 || it != 600 {
		t.Fatalf("in order months=%d total=%d, want 6 months of 100", ic, it)
	}

	// A catch-up that meets a closed month skips just that asset; the run itself still succeeds.
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2026-12-31"}`, owner), 200)
	late := asset("Late", 1200, 12, "2026-01-02")
	fresh := asset("Fresh", 1200, 12, "2027-01-05")
	res := expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2027-03"}`, owner), 200)
	if c, _ := months(late); c != 0 {
		t.Fatalf("closed-month asset posted %d months", c)
	}
	marked := false
	for _, raw := range res["skipped_closed_period"].([]any) {
		if int64(raw.(float64)) == late {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("skipped list %v does not name the January asset", res["skipped_closed_period"])
	}
	if c, total := months(fresh); c != 3 || total != 300 {
		t.Fatalf("fresh asset months=%d total=%d, want 3 months of 100", c, total)
	}
}
