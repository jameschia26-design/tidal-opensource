package main

import "testing"

func TestCompanyEvents(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "events-owner@example.com")
	outsider := seedSession(t, a, "events-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Events co","fiscal_year_end":"12-31"}`)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Event customer"}`, owner), 201)
	path := "/api/v1/events?org_id=" + itoa(org)
	result := expectStatus(t, do(t, h, "GET", path, "", owner), 200)
	rows := result["events"].([]any)
	if len(rows) < 2 {
		t.Fatal("missing events", rows)
	}
	first := rows[0].(map[string]any)
	if first["type"] != "contact.created" || first["source"] != "Tidal" || first["occurred_at"] == "" {
		t.Fatal(first)
	}
	page := expectStatus(t, do(t, h, "GET", path+"&before_id="+itoa(int64(first["id"].(float64))), "", owner), 200)
	if len(page["events"].([]any)) != len(rows)-1 {
		t.Fatal(page)
	}
	expectStatus(t, do(t, h, "GET", path+"&before_id=0", "", owner), 400)
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)
}
