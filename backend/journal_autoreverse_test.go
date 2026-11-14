package main

import "testing"

// An auto-reversing manual journal writes its reversal at creation time, dated on the chosen day, with the
// debits and credits swapped. Bad dates are refused and nothing is posted.
func TestJournalAutoReverse(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "autorev@example.com")
	org := orgFromList(t, a, owner, `{"name":"Auto reverse","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	mk := func(code, typ string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"`+code+`","name":"A `+code+`","type":"`+typ+`"}`, owner), 201)["id"].(float64))
	}
	asset, expense := mk("1250", "asset"), mk("6100", "expense")
	body := func(reverse, key string) string {
		return `{"org_id":` + o + `,"entry_date":"2026-03-01","memo":"Accrual"` + reverse + `,"idempotency_key":"` + key + `","lines":[{"account_id":` + itoa(expense) + `,"debit_cents":5000},{"account_id":` + itoa(asset) + `,"credit_cents":5000}]}`
	}
	for _, bad := range []string{`,"reversal_date":"2026-03-01"`, `,"reversal_date":"2026-02-01"`, `,"reversal_date":"nope"`} {
		if rec := do(t, h, "POST", "/api/v1/journals", body(bad, "bad"), owner); rec.Code != 400 {
			t.Fatalf("%s accepted: %d", bad, rec.Code)
		}
	}
	// Nothing was written by the refused attempts.
	if list := expectStatus(t, do(t, h, "GET", "/api/v1/journals?org_id="+o, "", owner), 200)["journals"].([]any); len(list) != 0 {
		t.Fatalf("refused journal wrote rows: %v", list)
	}
	created := expectStatus(t, do(t, h, "POST", "/api/v1/journals", body(`,"reversal_date":"2026-04-01"`, "accrual"), owner), 201)
	revID := int64(created["reversal_id"].(float64))
	if revID == 0 {
		t.Fatalf("no reversal written: %v", created)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/journals?org_id="+o, "", owner), 200)["journals"].([]any)
	if len(list) != 2 {
		t.Fatalf("journals = %d, want 2", len(list))
	}
	var sawOriginal, sawReversal bool
	for _, row := range list {
		m := row.(map[string]any)
		if int64(m["id"].(float64)) == revID {
			sawReversal = m["entry_date"] == "2026-04-01" && m["reversal_of"].(float64) == created["id"].(float64)
		} else if m["reversal_of"].(float64) == 0 {
			sawOriginal = m["entry_date"] == "2026-03-01"
		}
	}
	if !sawOriginal || !sawReversal {
		t.Fatalf("dates or links wrong: %v", list)
	}
	// The reversal swaps the sides, so both journals hold 50.00 on each side and the books still balance.
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/journals/"+itoa(revID)+"?org_id="+o, "", owner), 200)["lines"].([]any)
	if len(lines) != 2 {
		t.Fatalf("reversal lines = %v", lines)
	}
	if l := lines[0].(map[string]any); l["account_id"].(float64) != float64(expense) || l["credit_cents"].(float64) != 5000 {
		t.Fatalf("expense line not credited: %v", l)
	}
}
