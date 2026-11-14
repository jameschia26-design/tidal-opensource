package main

import (
	"context"
	"testing"
)

func TestCompanyBooksIsolationAndLimits(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "books-owner@example.com")
	outsider := seedSession(t, a, "books-outsider@example.com")
	viewer := seedSession(t, a, "books-viewer@example.com")
	company := orgFromList(t, a, owner, `{"name":"One company","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='books-viewer@example.com'`, company); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/organizations/" + itoa(company) + "/books"
	body := `{"name":"Retail books","fiscal_year_end":"12-31"}`
	expectStatus(t, do(t, h, "POST", path, body, outsider), 403)
	expectStatus(t, do(t, h, "POST", path, body, viewer), 403)
	book := expectStatus(t, do(t, h, "POST", path, body, owner), 201)
	id := int64(book["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/organizations", `{"name":"Second company","fiscal_year_end":"12-31"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/organizations/"+itoa(id)+"/books", body, owner), 400)
	for _, token := range []string{owner, viewer} {
		orgs := expectStatus(t, do(t, h, "GET", "/api/v1/organizations", "", token), 200)["organizations"].([]any)
		if len(orgs) != 2 {
			t.Fatalf("expected company and books: %v", orgs)
		}
		for _, raw := range orgs {
			got := raw.(map[string]any)
			if got["fiscal_year_end"] != "12-31" {
				t.Fatalf("fiscal year end missing for company/book: %v", got)
			}
		}
	}
	// Books may have different financial year ends. Organization list must expose each
	// selected entity's setting, not the parent company's value.
	if _, err := pool.Exec(context.Background(), `UPDATE organizations SET fiscal_year_end='06-30' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{owner, viewer} {
		orgs := expectStatus(t, do(t, h, "GET", "/api/v1/organizations", "", token), 200)["organizations"].([]any)
		for _, raw := range orgs {
			got := raw.(map[string]any)
			want := "12-31"
			if got["is_book"] == true {
				want = "06-30"
			}
			if got["fiscal_year_end"] != want {
				t.Fatalf("fiscal year end = %v, want %s: %v", got["fiscal_year_end"], want, got)
			}
		}
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/members?org_id="+itoa(id), "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(id)+`,"role":"customer","name":"Retail only"}`, owner), 201)
	mainContacts := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?role=customer&org_id="+itoa(company), "", owner), 200)["contacts"].([]any)
	if len(mainContacts) != 0 {
		t.Fatalf("books leaked contacts: %v", mainContacts)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/contacts?role=customer&org_id="+itoa(id), "", outsider), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(id)+`,"role":"customer","name":"Viewer denied"}`, viewer), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/contacts?role=customer", "", owner), 400)
	if all := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(company), "", owner), 200)["contacts"].([]any); len(all) < len(mainContacts) {
		t.Fatalf("blank role must list everyone: %d < %d", len(all), len(mainContacts))
	}
}
