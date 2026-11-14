package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCompanyValidation(t *testing.T) {
	valid := orgInput{Name: " Company ", UEN: "201234567a", FiscalYearEnd: "02-29", companyProfile: companyProfile{ContactEmail: "Finance@Example.com", Address: contactAddress{Country: "SG", PostalCode: "238801"}}}
	if !validCompany(&valid) || valid.Name != "Company" || valid.UEN != "201234567A" || valid.ContactEmail != "finance@example.com" {
		t.Fatal("normalisation failed")
	}
	for _, change := range []func(*orgInput){
		func(v *orgInput) { v.FiscalYearEnd = "02-30" }, func(v *orgInput) { v.BusinessType = "unknown" },
		func(v *orgInput) { v.ContactEmail = "broken" }, func(v *orgInput) { v.Industry = strings.Repeat("x", 101) },
		func(v *orgInput) { v.Name = "\x00" }, func(v *orgInput) { v.Website = "javascript:alert(1)" },
		func(v *orgInput) { v.Description = strings.Repeat("x", 501) }, func(v *orgInput) { v.Facebook = "javascript:alert(1)" },
		func(v *orgInput) { v.Physical.Country = "SGP" },
	} {
		v := valid
		change(&v)
		if validCompany(&v) {
			t.Fatal("invalid company accepted")
		}
	}
}

func TestCompanyProfileSaveAndPermissions(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "company-owner@example.com")
	viewer := seedSession(t, a, "company-viewer@example.com")
	outsider := seedSession(t, a, "company-outsider@example.com")
	body := `{"name":"Company profile","uen":"201234567a","fiscal_year_end":"12-31","legal_name":"Company Pte Ltd","contact_email":"Finance@Example.com","business_type":"company","description":"We publish software.","mobile":"+6581234567","fax":"+6561234567","facebook":"https://facebook.com/acme","twitter":"https://x.com/acme","linkedin":"https://linkedin.com/company/acme","address":{"country":"SG","postal_code":"238801","line1":"10 Orchard Road"},"physical_address":{"country":"SG","postal_code":"408600","line1":"20 Ubi Avenue 1"}}`
	rec := do(t, h, http.MethodPost, "/api/v1/organizations", body, owner)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	var org struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/organizations/" + itoa(org.ID)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='company-viewer@example.com'`, org.ID); err != nil {
		t.Fatal(err)
	}
	rec = do(t, h, http.MethodGet, path, "", owner)
	var saved orgInput
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || saved.Address.Line1 != "10 Orchard Road" || saved.ContactEmail != "finance@example.com" || saved.UEN != "201234567A" ||
		saved.Physical.Line1 != "20 Ubi Avenue 1" || saved.Mobile != "+6581234567" || saved.Description != "We publish software." || saved.Linkedin != "https://linkedin.com/company/acme" {
		t.Fatal(rec.Code, rec.Body)
	}
	updated := strings.Replace(body, "Company profile", "Renamed company", 1)
	for _, token := range []string{viewer, outsider} {
		if rec := do(t, h, http.MethodPut, path, updated, token); rec.Code != 403 {
			t.Fatal("write allowed", rec.Code, rec.Body)
		}
	}
	if rec := do(t, h, http.MethodGet, path, "", viewer); rec.Code != 200 {
		t.Fatal("viewer cannot read", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, path, "", outsider); rec.Code != 403 {
		t.Fatal("outsider can read", rec.Code)
	}
	if rec := do(t, h, http.MethodPut, path, updated, owner); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPut, path, strings.Replace(updated, `"12-31"`, `"06-30"`, 1), owner); rec.Code != 409 {
		t.Fatal("year end silently changed", rec.Code)
	}
	// Registering without gst_effective_from is refused (400), so tax status cannot change silently.
	if rec := do(t, h, http.MethodPut, path, strings.Replace(updated, `"legal_name"`, `"gst_registered":true,"legal_name"`, 1), owner); rec.Code != 400 {
		t.Fatal("tax status silently changed", rec.Code)
	}
	rec = do(t, h, http.MethodGet, path, "", owner)
	if !strings.Contains(rec.Body.String(), "Renamed company") || !strings.Contains(rec.Body.String(), `"fiscal_year_end":"12-31"`) {
		t.Fatal(rec.Body)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='organization.updated'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("audit missing", count, err)
	}
}

func TestCompanyGSTRegistrationHistory(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-history@example.com")
	body := `{"name":"GST history","uen":"","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2026-01-01","gst_number":"M12345678X"}`
	rec := do(t, h, http.MethodPost, "/api/v1/organizations", body, owner)
	if rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	var org struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/organizations/" + itoa(org.ID)
	rec = do(t, h, http.MethodGet, path, "", owner)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"gst_effective_from":"2026-01-01"`) {
		t.Fatal(rec.Code, rec.Body)
	}
	// Existing registration status and bills remain usable after migration, without invented history.
	// Legacy state (registered, no dated history) is seeded directly; the create API refuses it.
	var legacy int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations(name,owner_user_id,gst_registered,gst_registration_history_missing) SELECT 'Legacy GST',id,true,true FROM users WHERE email='gst-history@example.com' RETURNING id`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'owner' FROM users WHERE email='gst-history@example.com'`, legacy); err != nil {
		t.Fatal(err)
	}
	legacyProfile := `{"name":"Legacy GST updated","uen":"","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"","gst_number":""}`
	if rec := do(t, h, http.MethodPut, "/api/v1/organizations/"+itoa(legacy), legacyProfile, owner); rec.Code != http.StatusOK {
		t.Fatal("legacy profile edit", rec.Code, rec.Body)
	}
	legacyOff := `{"name":"Legacy GST updated","uen":"","fiscal_year_end":"12-31","gst_registered":false,"gst_effective_from":"","gst_number":""}`
	if rec := do(t, h, http.MethodPut, "/api/v1/organizations/"+itoa(legacy), legacyOff, owner); rec.Code != http.StatusConflict {
		t.Fatal("legacy status change needs real registration boundary", rec.Code, rec.Body)
	}
	// Registration starts 2026-01-01, ends on day before deregistration effective date.
	updated := strings.Replace(body, `"gst_registered":true,"gst_effective_from":"2026-01-01"`, `"gst_registered":false,"gst_effective_from":"2026-05-01"`, 1)
	rec = do(t, h, http.MethodPut, path, updated, owner)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gst_registrations WHERE org_id=$1`, org.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("registration history not retained", count, err)
	}
	var from, to string
	if err := pool.QueryRow(context.Background(), `SELECT effective_from::text,effective_to::text FROM gst_registrations WHERE org_id=$1`, org.ID).Scan(&from, &to); err != nil {
		t.Fatal(err)
	}
	if from != "2026-01-01" || to != "2026-04-30" {
		t.Fatalf("registration interval %s..%s", from, to)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE gst_registrations SET effective_to=NULL WHERE org_id=$1`, org.ID); err == nil {
		t.Fatal("append-only registration history mutated")
	}
}
