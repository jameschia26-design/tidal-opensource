package main

import "testing"

func TestContactAdditionalAddresses(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "addr-owner@example.com")
	other := seedSession(t, a, "addr-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Addr Co","fiscal_year_end":"12-31"}`)
	orgOther := orgFromList(t, a, other, `{"name":"Addr Other","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cid := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Multi Site"}`, owner), 201)["id"].(float64)))
	p := "/api/v1/contacts/" + cid + "/addresses"
	expectStatus(t, do(t, h, "POST", p+"?org_id="+o, `{"label":"Warehouse","address":{"line1":"1 Dock Rd","country":"SG","postal_code":"123456"}}`, owner), 201)
	expectStatus(t, do(t, h, "POST", p+"?org_id="+o, `{"label":"","address":{}}`, owner), 400)
	expectStatus(t, do(t, h, "POST", p+"?org_id="+o, `{"label":"Bad","address":{"country":"SG","postal_code":"12"}}`, owner), 400)
	expectStatus(t, do(t, h, "POST", p+"?org_id="+itoa(orgOther), `{"label":"x","address":{}}`, owner), 403)
	expectStatus(t, do(t, h, "GET", p+"?org_id="+itoa(orgOther), "", owner), 403)
	list := expectStatus(t, do(t, h, "GET", p+"?org_id="+o, "", owner), 200)["addresses"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["address"].(map[string]any)["line1"] != "1 Dock Rd" {
		t.Fatal(list)
	}
	aid := itoa(int64(list[0].(map[string]any)["id"].(float64)))
	expectStatus(t, do(t, h, "DELETE", "/api/v1/contact-addresses/"+aid+"?org_id="+itoa(orgOther), "", other), 404)
	expectStatus(t, do(t, h, "DELETE", "/api/v1/contact-addresses/"+aid+"?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "DELETE", "/api/v1/contact-addresses/"+aid+"?org_id="+o, "", owner), 404)
}
