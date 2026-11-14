package main

import (
	"strings"
	"testing"
)

func TestBankRulesSuggest(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "rule-owner@example.com")
	viewer := seedSession(t, a, "rule-viewer@example.com")
	outsider := seedSession(t, a, "rule-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Rules co","fiscal_year_end":"12-31"}`)
	other := orgFromList(t, a, outsider, `{"name":"Other co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='rule-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6400","name":"Bank charges","type":"expense"}`, owner), 201)
	aid := int64(acct["id"].(float64))
	foreign := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(other)+`,"code":"6400","name":"Foreign","type":"expense"}`, outsider), 201)
	body := func(name, dir, text string, acc int64) string {
		return `{"org_id":` + itoa(org) + `,"name":"` + name + `","direction":"` + dir + `","contains_text":"` + text + `","account_id":` + itoa(acc) + `}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body("Fee", "spend", "bank fee", aid), viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body("Fee", "spend", "bank fee", int64(foreign["id"].(float64))), owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body("Fee", "sideways", "bank fee", aid), owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body(" ", "spend", "bank fee", aid), owner), 400)
	r := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body("Fee", "spend", "BANK FEE", aid), owner), 201)
	csv := "date,description,reference,amount\n2026-11-07,Monthly bank fee,F1,-5.50\n2026-11-08,Bank fee refund,F2,5.50\n2026-11-09,Other,F3,-1.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), csv, owner), 200)
	suggested := func() map[string]any {
		out := map[string]any{}
		for _, l := range expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+itoa(org), "", viewer), 200)["lines"].([]any) {
			m := l.(map[string]any)
			out[m["reference"].(string)] = m["suggested_account_id"]
		}
		return out
	}
	s := suggested()
	if s["F1"] != float64(aid) || s["F2"] != nil || s["F3"] != nil {
		t.Fatal(s)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules/active", `{"org_id":`+itoa(org)+`,"id":`+itoa(int64(r["id"].(float64)))+`,"active":false}`, owner), 200)
	if suggested()["F1"] != nil {
		t.Fatal("inactive rule still suggests")
	}
	if rules := expectStatus(t, do(t, h, "GET", "/api/v1/bank/rules?org_id="+itoa(org), "", viewer), 200)["rules"].([]any); len(rules) != 1 {
		t.Fatal(rules)
	}
	r2 := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", body("Second", "spend", "other", aid), owner), 201)
	rid2 := int64(r2["id"].(float64))
	// Edit: text changes take effect; viewer and other-company users cannot edit.
	update := `{"org_id":` + itoa(org) + `,"name":"Second","direction":"spend","contains_text":"OTHER","account_id":` + itoa(aid) + `}`
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(rid2), update, owner), 200)
	if suggested()["F3"] != float64(aid) {
		t.Fatal("edited rule does not match")
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(rid2), update, viewer), 403)
	foreignUpdate := strings.NewReplacer(itoa(org), itoa(other), itoa(aid), itoa(int64(foreign["id"].(float64)))).Replace(update)
	// Another company's owner sees a plain not-found; the rule belongs to a different company.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(rid2), foreignUpdate, outsider), 404)
	otherRule := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+itoa(other)+`,"name":"X","direction":"spend","contains_text":"x","account_id":`+itoa(int64(foreign["id"].(float64)))+`}`, outsider), 201)
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(int64(otherRule["id"].(float64))), update, owner), 404)
	// Delete: removed rule stops suggesting; other-company rule untouched.
	if rec := do(t, h, "DELETE", "/api/v1/bank/rules/"+itoa(rid2)+"?org_id="+itoa(org), "", owner); rec.Code != 204 {
		t.Fatal(rec.Code, rec.Body)
	}
	if suggested()["F3"] != nil {
		t.Fatal("deleted rule still suggests")
	}
	expectStatus(t, do(t, h, "DELETE", "/api/v1/bank/rules/"+itoa(int64(otherRule["id"].(float64)))+"?org_id="+itoa(org), "", owner), 404)
	if rec := do(t, h, "DELETE", "/api/v1/bank/rules/"+itoa(rid2)+"?org_id="+itoa(org), "", viewer); rec.Code != 403 {
		t.Fatal(rec.Code, rec.Body)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/bank/rules?org_id="+itoa(org), "", outsider), 403)
}

func TestBankRuleConditionsAndPriority(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "rulecond-owner@example.com")
	viewer := seedSession(t, a, "rulecond-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"RuleCond co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='rulecond-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6400","name":"Bank charges","type":"expense"}`, owner), 201)
	aid := itoa(int64(acct["id"].(float64)))
	// First-active-by-id still applies when no priorities are sent (both default to 100).
	wide := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+itoa(org)+`,"name":"Wide","direction":"spend","contains_text":"fee","account_id":`+aid+`}`, owner), 201)
	_ = wide
	narrow := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+itoa(org)+`,"name":"Narrow","direction":"spend","contains_text":"fee","account_id":`+aid+`,"amount_min_cents":1000,"amount_max_cents":1000,"reference_contains":"VIP","priority":1}`, owner), 201)
	narrowID := itoa(int64(narrow["id"].(float64)))
	// Rejects: negative min, min above max, bad priority, bad reference.
	for _, f := range []string{
		`"amount_min_cents":-1`, `"amount_min_cents":2000,"amount_max_cents":1000`, `"priority":0`, `"priority":1000`,
	} {
		rec := do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+itoa(org)+`,"name":"Bad","direction":"spend","contains_text":"fee","account_id":`+aid+`,`+f+`}`, owner)
		if rec.Code != 400 {
			t.Fatalf("%s: got %d", f, rec.Code)
		}
	}
	// A blank reference becomes no condition instead of refusing the save.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+narrowID, `{"org_id":`+itoa(org)+`,"name":"Narrow","direction":"spend","contains_text":"fee","account_id":`+aid+`,"amount_min_cents":1000,"reference_contains":" ","priority":1}`, owner), 200)
	csv := "date,description,reference,amount\n2026-11-07,Monthly bank fee,X-1,-10.00\n2026-11-08,Monthly bank fee,X-2,-20.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), csv, owner), 200)
	suggested := map[string]any{}
	for _, l := range expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+itoa(org), "", viewer), 200)["lines"].([]any) {
		m := l.(map[string]any)
		suggested[m["reference"].(string)] = m["suggested_account_id"]
	}
	// X-1 matches the priority-1 rule (1000 >= 1000); X-2 fails the min and falls to the wide rule.
	if suggested["X-1"] != float64(int64(acct["id"].(float64))) || suggested["X-2"] != float64(int64(acct["id"].(float64))) {
		t.Fatal(suggested)
	}
	// Round trip: conditions show up when the rules are listed again.
	for _, x := range expectStatus(t, do(t, h, "GET", "/api/v1/bank/rules?org_id="+itoa(org), "", viewer), 200)["rules"].([]any) {
		m := x.(map[string]any)
		if m["name"] == "Narrow" {
			if m["priority"].(float64) != 1 || m["amount_min_cents"].(float64) != 1000 || m["reference_contains"] != nil {
				t.Fatalf("narrow round trip: %v", m)
			}
		}
	}
}

func TestBankRuleMultiCondition(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "multi-rule@example.com")
	org := orgFromList(t, a, owner, `{"name":"Multi rule co","fiscal_year_end":"12-31"}`)
	mk := func(code, name string) int64 {
		m := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"`+code+`","name":"`+name+`","type":"expense"}`, owner), 201)
		return int64(m["id"].(float64))
	}
	sales, gst := mk("6400", "Coffee wide"), mk("6410", "Coffee beans")

	// Narrow rule: description plus a reference condition.
	m := expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules?org_id="+itoa(org), `{"org_id":`+itoa(org)+`,"name":"Coffee","direction":"spend","contains_text":"coffee",
		"account_id":`+itoa(gst)+`,"conditions":[{"field":"reference","value":"CARD 42"}]}`, owner), 201)
	ruleID := int64(m["id"].(float64))
	// Wide rule with a worse priority number: only matches when the narrow one does not.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules?org_id="+itoa(org), `{"org_id":`+itoa(org)+`,"name":"Coffee wide","direction":"spend","contains_text":"coffee",
		"account_id":`+itoa(sales)+`,"priority":900}`, owner), 201)

	body := "date,description,reference,amount\n2026-02-01,CARD COFFEE SHOP,CARD 42 7810,-12.50\n2026-02-02,CARD COFFEE SHOP,ONLINE,-9.00\n"
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), body, owner), 200)
	suggested := func() map[string]any {
		out := map[string]any{}
		for _, raw := range expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+itoa(org), "", owner), 200)["lines"].([]any) {
			line := raw.(map[string]any)
			out[line["reference"].(string)] = line["suggested_account_id"]
		}
		return out
	}
	s := suggested()
	if s["CARD 42 7810"] != float64(gst) || s["ONLINE"] != float64(sales) {
		t.Fatalf("conditions not applied in priority order: %v", s)
	}
	rules := expectStatus(t, do(t, h, "GET", "/api/v1/bank/rules?org_id="+itoa(org), "", owner), 200)["rules"].([]any)
	for _, raw := range rules {
		r := raw.(map[string]any)
		if int64(r["id"].(float64)) == ruleID && len(r["conditions"].([]any)) != 1 {
			t.Fatalf("conditions not listed: %v", r)
		}
	}
	put := func(conds string) string {
		return `{"org_id":` + itoa(org) + `,"name":"Coffee","direction":"spend","contains_text":"coffee","account_id":` + itoa(gst) + `,"conditions":` + conds + `}`
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(ruleID), put(`[{"field":"payee","value":"x"}]`), owner), 400)
	// A blank condition row is dropped, so the rule widens and wins both lines by priority.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/rules/"+itoa(ruleID), put(`[{"field":"reference","value":" "}]`), owner), 200)
	if got := suggested(); got["ONLINE"] != float64(gst) {
		t.Fatalf("blank condition should be dropped: %v", got)
	}
}
