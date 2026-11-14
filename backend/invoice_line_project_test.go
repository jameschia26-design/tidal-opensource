package main

import "testing"

// A line can name a project. The project must belong to the invoice's customer and to the same company,
// and the chosen project comes back on the saved line.
func TestInvoiceLineProject(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "line-project@example.com")
	org := orgFromList(t, a, owner, `{"name":"Line projects","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	other := func() int64 {
		rec := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Other customer"}`, owner), 201)
		return int64(rec["id"].(float64))
	}()
	mkProject := func(contact int64, name string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+itoa(contact)+`,"name":"`+name+`"}`, owner), 201)["id"].(float64))
	}
	project := mkProject(cust, "Kitchen")
	foreignProject := mkProject(other, "Not this customer")
	base := func(extra string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-03-01","due_date":"2026-03-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":1000` + extra + `}]}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"project_id":999999`), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"project_id":`+itoa(foreignProject)), owner), 422)
	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(`,"project_id":`+itoa(project)), owner), 201)
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(int64(created["id"].(float64)))+"?org_id="+o, "", owner), 200)
	got := int64(detail["lines"].([]any)[0].(map[string]any)["project_id"].(float64))
	if got != project {
		t.Fatalf("project did not roundtrip: %d want %d", got, project)
	}
	// A line without a project keeps 0 rather than inheriting the header.
	plain := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", base(""), owner), 201)
	plainDetail := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(int64(plain["id"].(float64)))+"?org_id="+o, "", owner), 200)
	if v := plainDetail["lines"].([]any)[0].(map[string]any)["project_id"].(float64); v != 0 {
		t.Fatalf("plain line project = %v", v)
	}
}
