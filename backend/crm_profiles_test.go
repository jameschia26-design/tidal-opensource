package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCRMEditingAndDealProfiles(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "crm-edit-owner@example.com")
	viewer := seedSession(t, a, "crm-edit-viewer@example.com")
	outsider := seedSession(t, a, "crm-edit-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"CRM edit","fiscal_year_end":"12-31"}`)
	otherOrg := orgFromList(t, a, outsider, `{"name":"Other CRM","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='crm-edit-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	lead := expectStatus(t, do(t, h, "POST", "/api/v1/leads", `{"org_id":`+itoa(org)+`,"name":"Original"}`, owner), 201)
	id := int64(lead["id"].(float64))
	path := "/api/v1/leads/" + itoa(id)
	leadBody := `{"org_id":` + itoa(org) + `,"name":"Updated lead","email":"LEAD@example.com","source":"Referral","company_name":"Example Pte Ltd","phone":"61234567","job_title":"Director","website":"https://example.com","notes":"New notes"}`
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "PUT", path, leadBody, token), 403)
	}
	updated := expectStatus(t, do(t, h, "PUT", path, leadBody, owner), 200)
	if updated["email"] != "lead@example.com" || updated["notes"] != "New notes" || updated["stage"] != "new" {
		t.Fatal(updated)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/leads/999999", leadBody, owner), 404)
	expectStatus(t, do(t, h, "PUT", path, strings.Replace(leadBody, "New notes", `bad\u0000notes`, 1), owner), 400)
	customer := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := int64(customer["id"].(float64))
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Supplier"}`, owner), 201)
	foreign := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(otherOrg)+`,"role":"customer","name":"Foreign"}`, outsider), 201)
	foreignLead := expectStatus(t, do(t, h, "POST", "/api/v1/leads", `{"org_id":`+itoa(otherOrg)+`,"name":"Foreign lead"}`, outsider), 201)
	input := map[string]any{"org_id": org, "lead_id": id, "contact_id": cid, "title": "Implementation", "amount_cents": 123456, "expected_close_date": "2026-12-01", "probability": 75, "deal_type": "New business", "source": "Referral", "next_step": "Send quote", "notes": "Scope agreed"}
	body := func(v map[string]any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "POST", "/api/v1/deals", body(input), token), 403)
	}
	for _, invalid := range []struct {
		key   string
		value any
	}{
		{"lead_id", foreignLead["id"]}, {"lead_id", -1}, {"contact_id", supplier["id"]}, {"contact_id", foreign["id"]}, {"contact_id", 0}, {"probability", 101}, {"probability", -1}, {"probability", 0.5}, {"expected_close_date", "2026-02-30"}, {"notes", strings.Repeat("x", 4001)}, {"title", "bad\x00title"}, {"amount_cents", -1}, {"amount_cents", 100000000000001},
	} {
		v := make(map[string]any)
		for k, x := range input {
			v[k] = x
		}
		v[invalid.key] = invalid.value
		expectStatus(t, do(t, h, "POST", "/api/v1/deals", body(v), owner), 400)
	}
	deal := expectStatus(t, do(t, h, "POST", "/api/v1/deals", body(input), owner), 201)
	did := int64(deal["id"].(float64))
	dp := "/api/v1/deals/" + itoa(did)
	if deal["probability"] != float64(75) || deal["next_step"] != "Send quote" {
		t.Fatal(deal)
	}
	input["title"] = "Revised scope"
	input["amount_cents"] = 456789
	input["expected_close_date"] = ""
	input["probability"] = 90
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "PUT", dp, body(input), token), 403)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/deals/999999", body(input), owner), 404)
	expectStatus(t, do(t, h, "PUT", dp, body(input), owner), 200)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/deals?org_id="+itoa(org), "", viewer), 200)["deals"].([]any)
	d := listed[0].(map[string]any)
	if len(listed) != 1 || d["title"] != "Revised scope" || d["expected_close_date"] != "" || d["contact_id"] != float64(cid) || d["amount_cents"] != float64(456789) || d["notes"] != "Scope agreed" {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/deals?org_id="+itoa(org), "", outsider), 403)
	expectStatus(t, do(t, h, "POST", dp+"/stage", `{"org_id":`+itoa(org)+`,"stage":"won"}`, owner), 200)
	expectStatus(t, do(t, h, "PUT", dp, body(input), owner), 409)
	expectStatus(t, do(t, h, "POST", dp+"/stage", `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 422)
	expectStatus(t, do(t, h, "POST", path+"/stage", `{"org_id":`+itoa(org)+`,"stage":"qualified"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", path+"/convert", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`}`, owner), 201)
	expectStatus(t, do(t, h, "POST", path+"/stage", `{"org_id":`+itoa(org)+`,"stage":"lost"}`, owner), 409)
	// Editing lead never silently changes its linked customer or stage.
	updated = expectStatus(t, do(t, h, "PUT", path, leadBody, owner), 200)
	if updated["contact_id"] != float64(cid) || updated["stage"] != "qualified" {
		t.Fatal(updated)
	}
	input["contact_id"] = int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Other customer"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/deals", body(input), owner), 400)
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action IN ('deal.created','deal.updated','deal.stage_changed')`, org).Scan(&n); err != nil || n != 3 {
		t.Fatal("deal audit", n, err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&n); err != nil || n != 0 {
		t.Fatal("CRM posted money", n, err)
	}
}
