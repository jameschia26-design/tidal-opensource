package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestContactProfileValidation(t *testing.T) {
	valid := contactInput{Role: "both", contactProfile: contactProfile{ContactType: "business", UEN: "201234567a", Website: "https://example.com", PaymentTermsDays: 30, BillingAddress: contactAddress{Country: "SG", PostalCode: "238801"}}}
	if !validContactProfile(&valid) || valid.UEN != "201234567A" {
		t.Fatal("valid contact rejected or UEN not normalised")
	}
	for _, change := range []func(*contactInput){
		func(v *contactInput) { v.Role = "admin" },
		func(v *contactInput) { v.PaymentTermsDays = -1 },
		func(v *contactInput) { v.PaymentTermsDays = 366 },
		func(v *contactInput) { v.Website = "javascript:alert(1)" },
		func(v *contactInput) { v.Website = "https://user:pass@example.com" },
		func(v *contactInput) { v.BillingAddress.PostalCode = "123" },
		func(v *contactInput) { v.ShippingAddress.Country = "Singapore" },
		func(v *contactInput) { v.Notes = strings.Repeat("x", 4001) },
		func(v *contactInput) { v.ShippingAddress.PostalCode = "12\x003" },
		func(v *contactInput) { v.ShippingAddress.PostalCode = "\xff" },
	} {
		v := valid
		change(&v)
		if validContactProfile(&v) {
			t.Fatalf("invalid contact accepted: %+v", v)
		}
	}
	legacy := contactInput{Role: "customer"}
	if !validContactProfile(&legacy) || legacy.ContactType != "business" {
		t.Fatal("legacy payload no longer accepted")
	}
}

func TestContactProfileRoundTrip(t *testing.T) {
	a, _ := testApp(t)
	token := seedSession(t, a, "profile@example.com")
	h := a.routes()
	rec := do(t, h, http.MethodPost, "/api/v1/organizations", `{"name":"Profiles","fiscal_year_end":"12-31"}`, token)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	body := `{"role":"supplier","name":"Paper supplier","email":"orders@example.com","contact_type":"business","company_name":"Paper Pte Ltd","first_name":"Alex","phone":"+65 6123 4567","payment_terms_days":45,"billing_address":{"country":"SG","postal_code":"238801","line1":"10 Orchard Road"},"shipping_address":{"country":"SG"},"notes":"Monthly deliveries"}`
	rec = do(t, h, http.MethodPost, "/api/v1/contacts", body, token)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	rec = do(t, h, http.MethodGet, "/api/v1/contacts?role=supplier", "", token)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var result struct {
		Contacts []struct {
			Name string `json:"name"`
			contactProfile
		} `json:"contacts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Contacts) != 1 || result.Contacts[0].PaymentTermsDays != 45 || result.Contacts[0].BillingAddress.Line1 != "10 Orchard Road" || result.Contacts[0].CompanyName != "Paper Pte Ltd" {
		t.Fatalf("profile not saved: %s", rec.Body)
	}
}

func TestContactEditingAndAccess(t *testing.T) {
	a, pool := testApp(t)
	ctx := context.Background()
	h := a.routes()
	owner := seedSession(t, a, "owner@example.com")
	outsider := seedSession(t, a, "outsider@example.com")
	viewer := seedSession(t, a, "viewer@example.com")
	rec := do(t, h, http.MethodPost, "/api/v1/organizations", `{"name":"Editing","fiscal_year_end":"12-31"}`, owner)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	var org struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships (org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='viewer@example.com'`, org.ID); err != nil {
		t.Fatal(err)
	}
	body := `{"org_id":` + itoa(org.ID) + `,"role":"both","name":"Shared contact"}`
	rec = do(t, h, http.MethodPost, "/api/v1/contacts", body, owner)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	var contact struct {
		ID               int64 `json:"id"`
		PaymentTermsDays int   `json:"payment_terms_days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contact); err != nil {
		t.Fatal(err)
	}
	if contact.PaymentTermsDays != 0 {
		t.Fatal("missing payment terms must default to due on receipt, per map sections 5 and 6")
	}
	path := "/api/v1/contacts/" + itoa(contact.ID)
	updated := `{"org_id":` + itoa(org.ID) + `,"role":"both","name":"Updated contact","payment_terms_days":0,"notes":"Edited"}`
	for _, token := range []string{outsider, viewer} {
		if rec := do(t, h, http.MethodPut, path, updated, token); rec.Code != 403 {
			t.Fatalf("unauthorised edit: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := do(t, h, http.MethodPost, "/api/v1/contacts", body, viewer); rec.Code != 403 {
		t.Fatalf("viewer created contact: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/v1/contacts?role=supplier&org_id="+itoa(org.ID), "", outsider); rec.Code != 403 {
		t.Fatalf("outsider read contact: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, path, updated, owner); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	for _, role := range []string{"customer", "supplier"} {
		rec := do(t, h, http.MethodGet, "/api/v1/contacts?role="+role, "", owner)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"Updated contact"`) || !strings.Contains(rec.Body.String(), `"payment_terms_days":0`) || !strings.Contains(rec.Body.String(), `"notes":"Edited"`) {
			t.Fatalf("updated contact missing: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := do(t, h, http.MethodPut, "/api/v1/contacts/999999", updated, owner); rec.Code != 404 {
		t.Fatalf("missing contact: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPut, path, strings.Replace(updated, `"notes":"Edited"`, `"notes":"\u0000"`, 1), owner); rec.Code != 400 {
		t.Fatalf("invalid edit accepted: %d", rec.Code)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action IN ('contact.created','contact.updated')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count=%d error=%v", count, err)
	}
}
