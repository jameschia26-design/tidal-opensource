package main

import (
	"context"
	"sync"
	"testing"
)

// TestGSTRegistrationPeriodsCannotOverlap covers B13: the database, not a read-then-write check, refuses
// overlapping registration periods, so two concurrent saves cannot both succeed.
func TestGSTRegistrationPeriodsCannotOverlap(t *testing.T) {
	a, pool := testApp(t)
	owner := seedSession(t, a, "gst-overlap@example.com")
	org := orgFromList(t, a, owner, `{"name":"Overlap","fiscal_year_end":"12-31"}`)
	// First registration covers 2022; seeded directly so the fixture is not testing the settings API.
	if _, err := pool.Exec(t.Context(), `INSERT INTO gst_registrations(org_id,effective_from,effective_to) VALUES($1,'2022-01-01','2023-12-31')`, org); err != nil {
		t.Fatal(err)
	}

	// Two writers insert overlapping periods at the same time; exactly one must win.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, from := range []string{"2024-01-01", "2024-06-01"} {
		wg.Add(1)
		go func(from string) {
			defer wg.Done()
			_, err := pool.Exec(context.Background(),
				`INSERT INTO gst_registrations(org_id,effective_from) VALUES($1,$2::date)`, org, from)
			results <- err
		}(from)
	}
	wg.Wait()
	close(results)
	failures := 0
	for err := range results {
		if err != nil {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("%d inserts failed, want exactly 1 (one period wins)", failures)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM gst_registrations WHERE org_id=$1`, org).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("registration rows=%d, want the original plus one winner", count)
	}
}
