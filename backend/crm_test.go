package main

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestLeadConversionAndActivities(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "crm-owner@example.com")
	outsider := seedSession(t, a, "crm-outsider@example.com")
	viewer := seedSession(t, a, "crm-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"CRM company","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='crm-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	lead := expectStatus(t, do(t, h, "POST", "/api/v1/leads", `{"org_id":`+itoa(org)+`,"name":"Alex","email":"alex@example.com","company_name":"Alex Pte Ltd","phone":"61234567","job_title":"Director","website":"https://example.com","notes":"Interested"}`, owner), 201)
	id := int64(lead["id"].(float64))
	path := "/api/v1/leads/" + itoa(id) + "/convert"
	body := `{"org_id":` + itoa(org) + `}`
	if rec := do(t, h, "POST", path, body, owner); rec.Code != 409 {
		t.Fatal("unqualified conversion", rec.Code)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/leads/"+itoa(id)+"/stage", `{"org_id":`+itoa(org)+`,"stage":"qualified"}`, owner), 200)
	for _, token := range []string{outsider, viewer} {
		if rec := do(t, h, "POST", path, body, token); rec.Code != 403 {
			t.Fatal("conversion access", rec.Code)
		}
	}
	var wg sync.WaitGroup
	codes := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", path, body, owner).Code }()
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code != 200 {
			t.Fatal(code)
		}
	}
	if created != 1 {
		t.Fatal("duplicate contacts", created)
	}
	customers := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org)+"&role=customer", "", owner), 200)["contacts"].([]any)
	if len(customers) != 1 || customers[0].(map[string]any)["company_name"] != "Alex Pte Ltd" {
		t.Fatal(customers)
	}
	activities := `{"org_id":` + itoa(org) + `,"lead_id":` + itoa(id) + `,"kind":"call","subject":"Follow up","due_date":"2026-11-01","notes":"Discuss quote"}`
	for _, token := range []string{outsider, viewer} {
		if rec := do(t, h, "POST", "/api/v1/activities", activities, token); rec.Code != 403 {
			t.Fatal("activity write access", rec.Code)
		}
	}
	activity := expectStatus(t, do(t, h, "POST", "/api/v1/activities", activities, owner), 201)
	activityID := int64(activity["id"].(float64))
	if rec := do(t, h, "POST", "/api/v1/activities", strings.Replace(activities, `"lead_id":`+itoa(id), `"lead_id":999999`, 1), owner); rec.Code != 400 {
		t.Fatal("foreign lead activity", rec.Code)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/activities?org_id="+itoa(org)+"&lead_id="+itoa(id), "", viewer), 200)["activities"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["subject"] != "Follow up" {
		t.Fatal(listed)
	}
	complete := "/api/v1/activities/" + itoa(activityID) + "/complete"
	if rec := do(t, h, "POST", complete, body, viewer); rec.Code != 403 {
		t.Fatal("viewer completed", rec.Code)
	}
	expectStatus(t, do(t, h, "POST", complete, body, owner), 200)
	if rec := do(t, h, "GET", "/api/v1/activities?org_id="+itoa(org), "", outsider); rec.Code != 403 {
		t.Fatal("activity leak", rec.Code)
	}
	listed = expectStatus(t, do(t, h, "GET", "/api/v1/activities?org_id="+itoa(org), "", owner), 200)["activities"].([]any)
	if listed[0].(map[string]any)["completed"] != true {
		t.Fatal("completion not saved")
	}
}

func TestLeadConversionReusesContact(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "crm-link@example.com")
	org := orgFromList(t, a, owner, `{"name":"Existing contacts","fiscal_year_end":"12-31"}`)
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Existing","email":"existing@example.com"}`, owner), 201)
	lead := expectStatus(t, do(t, h, "POST", "/api/v1/leads", `{"org_id":`+itoa(org)+`,"name":"Existing","email":"existing@example.com"}`, owner), 201)
	id := int64(lead["id"].(float64))
	contactID := int64(contact["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/leads/"+itoa(id)+"/stage", `{"org_id":`+itoa(org)+`,"stage":"qualified"}`, owner), 200)
	path := "/api/v1/leads/" + itoa(id) + "/convert"
	if rec := do(t, h, "POST", path, `{"org_id":`+itoa(org)+`}`, owner); rec.Code != 409 {
		t.Fatal("duplicate identity created", rec.Code)
	}
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(contactID)+`}`, owner), 201)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org)+"&role=customer", "", owner), 200)["contacts"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["role"] != "both" {
		t.Fatal("supplier not promoted to both", listed)
	}
}
