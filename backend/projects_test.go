package main

import "testing"

func TestProjectsAndBillableExpense(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "proj-owner@example.com")
	other := seedSession(t, a, "proj-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Proj Co","fiscal_year_end":"12-31"}`)
	orgOther := orgFromList(t, a, other, `{"name":"Proj Other","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Site Client"}`, owner), 201)))
	cust2 := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Other Client"}`, owner), 201)))
	sup := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Supplier"}`, owner), 201)))
	exp := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Travel","type":"expense"}`, owner), 201)))
	bank := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1015","name":"Till","type":"asset"}`, owner), 201)))
	projResponse := expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Fit-out","estimated_cost_cents":12000,"estimated_charge_cents":25000}`, owner), 201)
	proj := itoa(id(projResponse))
	projectRow := func() map[string]any {
		projects := expectStatus(t, do(t, h, "GET", "/api/v1/projects?org_id="+o, "", owner), 200)["projects"].([]any)
		for _, raw := range projects {
			p := raw.(map[string]any)
			if int64(p["id"].(float64)) == id(projResponse) {
				return p
			}
		}
		t.Fatalf("created project missing: %v", projects)
		return nil
	}
	created := projectRow()
	if int64(created["estimated_cost_cents"].(float64)) != 12000 || int64(created["estimated_charge_cents"].(float64)) != 25000 {
		t.Fatalf("project estimates were not saved: %+v", created)
	}
	expectStatus(t, do(t, h, "PATCH", "/api/v1/projects/"+proj, `{"org_id":`+o+`,"estimated_cost_cents":45000,"estimated_charge_cents":80000}`, owner), 200)
	created = projectRow()
	if int64(created["estimated_cost_cents"].(float64)) != 45000 || int64(created["estimated_charge_cents"].(float64)) != 80000 {
		t.Fatalf("edited project estimates were not persisted: %+v", created)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Negative estimate","estimated_cost_cents":-1}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"Oversized estimate","estimated_charge_cents":999999999999999999}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+cust+`,"name":"fit-out"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+sup+`,"name":"Nope"}`, owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+itoa(orgOther)+`,"contact_id":`+cust+`,"name":"x"}`, owner), 403)
	if n := len(expectStatus(t, do(t, h, "GET", "/api/v1/projects?org_id="+o+"&contact_id="+cust2, "", owner), 200)["projects"].([]any)); n != 0 {
		t.Fatal(n)
	}
	expense := func(key, extra string, status int) map[string]any {
		return expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-10-15","payee":"Cab","description":"Ride","expense_account_id":`+exp+`,"paid_through_account_id":`+bank+`,"amount_cents":1500,"idempotency_key":"`+key+`"`+extra+`}`, owner), status)
	}
	expense("proj-key-001", `,"customer_id":`+cust2+`,"project_id":`+proj, 422) // project belongs to another customer
	expense("proj-key-002", `,"billable":true`, 400)                            // billable needs a customer
	expense("proj-key-003", `,"customer_id":`+cust+`,"project_id":`+proj+`,"billable":true`, 201)
	rows := expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+o, "", owner), 200)["expenses"].([]any)
	e := rows[0].(map[string]any)
	if e["billable"] != true || itoa(int64(e["project_id"].(float64))) != proj {
		t.Fatal(e)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+proj+"/active", `{"org_id":`+o+`,"active":false}`, owner), 200)
	expense("proj-key-004", `,"customer_id":`+cust+`,"project_id":`+proj, 422) // switched off
}
