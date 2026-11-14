package main

import "testing"

func TestCustomFieldsAndTags(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ex-owner@example.com")
	other := seedSession(t, a, "ex-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Extras","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"contacts","label":"Region","field_type":"text"}`, other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"nope","label":"Region","field_type":"text"}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"contacts","label":"Region","field_type":"blob"}`, owner), 400)
	f := id(expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"contacts","label":"Region","field_type":"text"}`, owner), 201))
	n := id(expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"contacts","label":"Seats","field_type":"number"}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields", `{"org_id":`+o+`,"entity_kind":"contacts","label":"region","field_type":"text"}`, owner), 409)
	tag := id(expectStatus(t, do(t, h, "POST", "/api/v1/reporting-tags", `{"org_id":`+o+`,"name":"Channel","options":["Web","Shop"]}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/reporting-tags", `{"org_id":`+o+`,"name":"Bad","options":["A","a"]}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/reporting-tags", `{"org_id":`+o+`,"name":"Bad","options":[]}`, owner), 400)

	path := "/api/v1/entities/contacts/" + itoa(cust) + "/extras"
	put := func(body string, want int) {
		expectStatus(t, do(t, h, "PUT", path, `{"org_id":`+o+`,`+body+`}`, owner), want)
	}
	put(`"custom":{"`+itoa(n)+`":"abc"}`, 422) // not a number
	put(`"custom":{"999999":"x"}`, 422)        // unknown field
	put(`"tags":{"`+itoa(tag)+`":"Phone"}`, 422)
	put(`"custom":{"`+itoa(f)+`":"North","`+itoa(n)+`":"12.5"},"tags":{"`+itoa(tag)+`":"Web"}`, 200)
	expectStatus(t, do(t, h, "PUT", path, `{"org_id":`+o+`,"custom":{}}`, other), 403)
	got := expectStatus(t, do(t, h, "GET", path+"?org_id="+o, "", owner), 200)
	if got["custom"].(map[string]any)[itoa(f)] != "North" || got["tags"].(map[string]any)[itoa(tag)] != "Web" {
		t.Fatal(got)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/custom-fields/"+itoa(f)+"/active", `{"org_id":`+o+`,"active":false}`, owner), 200)
	put(`"custom":{"`+itoa(f)+`":"South"}`, 422) // deactivated
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/custom-fields?org_id="+o+"&kind=contacts", "", owner), 200)["custom_fields"].([]any); len(l) != 2 {
		t.Fatal(l)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/contacts/999999/extras?org_id="+o, "", owner), 404)
}
