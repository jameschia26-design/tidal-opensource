package main

import (
	"strings"
	"testing"
)

func TestBrandingThemes(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "brand-owner@example.com")
	outsider := seedSession(t, a, "brand-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Brand co","fiscal_year_end":"12-31"}`)
	post := func(path, body, tok string) int { return do(t, h, "POST", path, body, tok).Code }
	o := `{"org_id":` + itoa(org) + `,`
	if c := post("/api/v1/branding-themes", o+`"name":"A","footer":"x"}`, outsider); c != 403 {
		t.Fatal("outsider", c)
	}
	if c := post("/api/v1/branding-themes", o+`"name":" ","footer":"x"}`, owner); c != 400 {
		t.Fatal("blank name", c)
	}
	first := expectStatus(t, do(t, h, "POST", "/api/v1/branding-themes", o+`"name":"Main","footer":"Pay by PayNow UEN 123","is_default":true}`, owner), 201)
	if c := post("/api/v1/branding-themes", o+`"name":"main","footer":""}`, owner); c != 409 {
		t.Fatal("duplicate name", c)
	}
	second := expectStatus(t, do(t, h, "POST", "/api/v1/branding-themes", o+`"name":"Alt","footer":"Alt footer"}`, owner), 201)
	if c := post("/api/v1/branding-themes/default", o+`"id":999999}`, owner); c != 404 {
		t.Fatal("missing theme", c)
	}
	list := func() []any {
		return expectStatus(t, do(t, h, "GET", "/api/v1/branding-themes?org_id="+itoa(org), "", owner), 200)["themes"].([]any)
	}
	defaults := func() (n int, id float64) {
		for _, x := range list() {
			m := x.(map[string]any)
			if m["is_default"] == true {
				n, id = n+1, m["id"].(float64)
			}
		}
		return
	}
	if n, id := defaults(); n != 1 || id != first["id"].(float64) {
		t.Fatal(n, id)
	}
	if c := post("/api/v1/branding-themes/default", o+`"id":`+itoa(int64(second["id"].(float64)))+`}`, owner); c != 200 {
		t.Fatal(c)
	}
	if n, id := defaults(); n != 1 || id != second["id"].(float64) {
		t.Fatal(n, id)
	}
	// default footer prints on a document PDF
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", o+`"role":"customer","name":"C"}`, owner), 201)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", o+`"contact_id":`+itoa(int64(c["id"].(float64)))+`,"invoice_date":"2026-01-01","due_date":"2026-02-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":100}]}`, owner), 201)
	rec := do(t, h, "GET", "/api/v1/documents/invoices/"+itoa(int64(inv["id"].(float64)))+"/pdf?org_id="+itoa(org), "", owner)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Alt footer") {
		t.Fatal("footer missing", rec.Code)
	}
}

func TestBrandingThemeEditDelete(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "brand2-owner@example.com")
	viewer := seedSession(t, a, "brand2-viewer@example.com")
	outsider := seedSession(t, a, "brand2-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Brand2 co","fiscal_year_end":"12-31"}`)
	o := `{"org_id":` + itoa(org) + `,`
	main := expectStatus(t, do(t, h, "POST", "/api/v1/branding-themes", o+`"name":"Main","footer":"Main footer","is_default":true}`, owner), 201)
	alt := expectStatus(t, do(t, h, "POST", "/api/v1/branding-themes", o+`"name":"Alt","footer":"Alt footer"}`, owner), 201)
	altID := itoa(int64(alt["id"].(float64)))
	put := func(path, body, tok string) int { return do(t, h, "PUT", path, body, tok).Code }
	if c := put("/api/v1/branding-themes/"+altID, o+`"name":"Alt","footer":"x"}`, outsider); c != 403 {
		t.Fatal("outsider edit", c)
	}
	if c := put("/api/v1/branding-themes/"+altID, o+`"name":"Alt","footer":"x"}`, viewer); c != 403 {
		t.Fatal("viewer edit", c)
	}
	if c := put("/api/v1/branding-themes/999999", o+`"name":"Ghost","footer":"g"}`, owner); c != 404 {
		t.Fatal("missing edit", c)
	}
	if c := put("/api/v1/branding-themes/"+altID, o+`"name":"MAIN","footer":"clash"}`, owner); c != 409 {
		t.Fatal("name clash", c)
	}
	themes := expectStatus(t, do(t, h, "GET", "/api/v1/branding-themes?org_id="+itoa(org), "", owner), 200)["themes"].([]any)
	for _, x := range themes {
		m := x.(map[string]any)
		if m["id"].(float64) == alt["id"].(float64) && m["footer"] != "Alt footer" {
			t.Fatalf("clash write leaked: %v", m)
		}
	}
	if c := put("/api/v1/branding-themes/"+altID, o+`"name":"Evening","footer":"Evening footer"}`, owner); c != 200 {
		t.Fatal("edit", c)
	}
	themes = expectStatus(t, do(t, h, "GET", "/api/v1/branding-themes?org_id="+itoa(org), "", owner), 200)["themes"].([]any)
	found := false
	for _, x := range themes {
		m := x.(map[string]any)
		if m["id"].(float64) == alt["id"].(float64) && m["name"] == "Evening" && m["footer"] == "Evening footer" {
			found = true
		}
	}
	if !found {
		t.Fatal("edited row missing")
	}
	del := func(path, tok string) int { return do(t, h, "DELETE", path, "", tok).Code }
	if c := del("/api/v1/branding-themes/"+altID+"?org_id="+itoa(org), outsider); c != 403 {
		t.Fatal("outsider delete", c)
	}
	if c := del("/api/v1/branding-themes/"+altID+"?org_id="+itoa(org), viewer); c != 403 {
		t.Fatal("viewer delete", c)
	}
	if c := del("/api/v1/branding-themes/"+altID+"?org_id="+itoa(org+999), owner); c != 403 {
		t.Fatal("foreign delete", c)
	}
	mainID := itoa(int64(main["id"].(float64)))
	if c := del("/api/v1/branding-themes/"+mainID+"?org_id="+itoa(org), owner); c != 409 {
		t.Fatal("default delete", c)
	}
	if c := del("/api/v1/branding-themes/"+altID+"?org_id="+itoa(org), owner); c != 200 {
		t.Fatal("delete", c)
	}
	for _, x := range expectStatus(t, do(t, h, "GET", "/api/v1/branding-themes?org_id="+itoa(org), "", owner), 200)["themes"].([]any) {
		if x.(map[string]any)["id"].(float64) == alt["id"].(float64) {
			t.Fatal("row survived delete")
		}
	}
}
