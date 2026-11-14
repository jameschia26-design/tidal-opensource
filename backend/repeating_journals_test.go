package main

import "testing"

func TestRepeatingJournalRunsOnceAndStaysBalanced(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "repjrnl-owner@example.com")
	outsider := seedSession(t, a, "repjrnl-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Repeat co","fiscal_year_end":"12-31"}`)
	mk := func(code, typ string) int64 {
		m := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"`+code+`","name":"A`+code+`","type":"`+typ+`"}`, owner), 201)
		return int64(m["id"].(float64))
	}
	dr, cr := mk("6199", "expense"), mk("2199", "liability")
	lines := `"lines":[{"account_id":` + itoa(dr) + `,"description":"Rent accrual","debit_cents":1000},{"account_id":` + itoa(cr) + `,"credit_cents":1000}]`
	base := `{"org_id":` + itoa(org) + `,"narration":"Accrual","repeat_every":1,"repeat_unit":"month","start_date":"2026-01-31",`
	for name, bad := range map[string]string{
		"unbalanced": base + `"lines":[{"account_id":` + itoa(dr) + `,"debit_cents":1000},{"account_id":` + itoa(cr) + `,"credit_cents":900}]}`,
		"no text":    `{"org_id":` + itoa(org) + `,"narration":" ","repeat_every":1,"repeat_unit":"month","start_date":"2026-01-31",` + lines + `}`,
		"bad unit":   `{"org_id":` + itoa(org) + `,"narration":"x","repeat_every":1,"repeat_unit":"day","start_date":"2026-01-31",` + lines + `}`,
		"bad end":    base + `"ends_on":"2026-01-01",` + lines + `}`,
	} {
		if rec := do(t, h, "POST", "/api/v1/repeating-journals", bad, owner); rec.Code != 400 {
			t.Fatal(name, rec.Code)
		}
	}
	if rec := do(t, h, "POST", "/api/v1/repeating-journals", base+lines+`}`, outsider); rec.Code != 403 {
		t.Fatal("outsider created", rec.Code)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/repeating-journals", base+`"ends_on":"2026-03-31",`+lines+`}`, owner), 201)
	run := `{"org_id":` + itoa(org) + `,"as_of":"2026-12-31"}`
	first := expectStatus(t, do(t, h, "POST", "/api/v1/repeating-journals/run", run, owner), 200)
	if n := len(first["created_journal_ids"].([]any)); n != 3 { // 31 Jan, 28 Feb, 31 Mar
		t.Fatal("expected 3 journals, got", n)
	}
	again := expectStatus(t, do(t, h, "POST", "/api/v1/repeating-journals/run", run, owner), 200)
	if n := len(again["created_journal_ids"].([]any)); n != 0 {
		t.Fatal("repeat run posted", n)
	}
	if rec := do(t, h, "POST", "/api/v1/repeating-journals/run", run, outsider); rec.Code != 403 {
		t.Fatal("outsider ran", rec.Code)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/repeating-journals?org_id="+itoa(org), "", owner), 200)
	p := list["profiles"].([]any)[0].(map[string]any)
	if p["active"] != false || p["runs_done"].(float64) != 3 {
		t.Fatal(p)
	}
	jl := expectStatus(t, do(t, h, "GET", "/api/v1/journals?org_id="+itoa(org), "", owner), 200)
	jid := int64(jl["journals"].([]any)[0].(map[string]any)["id"].(float64))
	det := expectStatus(t, do(t, h, "GET", "/api/v1/journals/"+itoa(jid)+"?org_id="+itoa(org), "", owner), 200)
	if det["lines"].([]any)[0].(map[string]any)["description"] != "Rent accrual" {
		t.Fatal("line description lost", det)
	}
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+itoa(org)+"&as_of=2026-12-31", "", owner), 200)
	if tb["balanced"] != true || tb["total_debit_cents"].(float64) != 3000 {
		t.Fatal(tb)
	}
}
