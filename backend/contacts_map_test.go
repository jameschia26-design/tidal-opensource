package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Opening balance posts once, a changed amount is refused, and the owner must be a member.
func TestContactOpeningBalanceAndOwner(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "contact-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Contact co","fiscal_year_end":"12-31"}`)

	base := `{"org_id":` + itoa(org) + `,"role":"customer","name":"Opening customer","opening_balance_cents":5000,"opening_balance_date":"2026-01-01"`
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", base+`}`, owner), 201)
	cid := itoa(int64(c["id"].(float64)))

	// Exactly one opening journal per contact: Dr AR 1100, Cr 3900.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journals WHERE org_id=$1 AND memo='Opening balance'`, org).Scan(&n); err != nil || n != 1 {
		t.Fatalf("opening journals=%d err=%v", n, err)
	}

	// Editing to a different amount is refused; the books are not rewritten.
	expectStatus(t, do(t, h, "PUT", "/api/v1/contacts/"+cid, base+`,"opening_balance_cents":9999}`, owner), 409)
	expectStatus(t, do(t, h, "PUT", "/api/v1/contacts/"+cid, strings.Replace(base, `"opening_balance_cents":5000`, `"opening_balance_cents":0`, 1)+`}`, owner), 409)
	expectStatus(t, do(t, h, "PUT", "/api/v1/contacts/"+cid, strings.Replace(base, "2026-01-01", "2026-01-02", 1)+`}`, owner), 409)
	// Re-saving the same amount is accepted and posts nothing new.
	expectStatus(t, do(t, h, "PUT", "/api/v1/contacts/"+cid, base+`}`, owner), 200)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journals WHERE org_id=$1 AND memo='Opening balance'`, org).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after resave opening journals=%d err=%v", n, err)
	}

	listed := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?role=customer&org_id="+itoa(org), "", owner), 200)
	contact := listed["contacts"].([]any)[0].(map[string]any)
	if contact["opening_balance_date"] != "2026-01-01" || contact["currency"] != "SGD" || contact["contact_persons"] == nil {
		t.Fatalf("contact fields not retained: %v", contact)
	}
	// Owner must be a member of this company.
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Bad owner","owner_user_id":999999}`, owner), 422)
}

func TestContactMapRoundTripAndMembers(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "mapped-owner@example.com")
	outsider := seedSession(t, a, "outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Mapped co","fiscal_year_end":"12-31"}`)
	members := expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+itoa(org), "", owner), 200)["members"].([]any)
	if len(members) != 1 || members[0].(map[string]any)["email"] != "mapped-owner@example.com" {
		t.Fatalf("wrong members: %v", members)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+itoa(org), "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id=-1", "", owner), 400)
	body := `{"org_id":` + itoa(org) + `,"role":"supplier","name":"Mapped vendor","currency":"SGD","salutation":"Ms","language":"zh","portal_enabled":true,"owner_user_id":` + itoa(int64(members[0].(map[string]any)["id"].(float64))) + `,"internal_remarks":"Private note","billing_address":{"phone":"+65 61234567","fax":"+65 61234568"},"contact_persons":[{"first_name":"Alex","email":"alex@example.com","work_phone":"+65 61234567"}]}`
	created := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", body, owner), 201)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?role=supplier&org_id="+itoa(org), "", owner), 200)["contacts"].([]any)[0].(map[string]any)
	for _, key := range []string{"currency", "salutation", "language", "portal_enabled", "owner_user_id", "internal_remarks", "billing_address", "contact_persons"} {
		want, _ := json.Marshal(created[key])
		got, _ := json.Marshal(listed[key])
		if string(want) != string(got) {
			t.Errorf("%s: got %s want %s", key, got, want)
		}
	}
	for _, bad := range []string{
		strings.Replace(body, `"currency":"SGD"`, `"currency":"USD"`, 1),
		strings.Replace(body, `"email":"alex@example.com"`, `"email":"invalid"`, 1),
		strings.Replace(body, `"currency":"SGD"`, `"opening_balance_date":"2026-02-30"`, 1),
	} {
		expectStatus(t, do(t, h, "POST", "/api/v1/contacts", bad, owner), 400)
	}
}
