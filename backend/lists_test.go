package main

import "testing"

func TestSalespeopleAndPaymentTerms(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ls-owner@example.com")
	other := seedSession(t, a, "ls-other@example.com")
	o := itoa(orgFromList(t, a, owner, `{"name":"Lists","fiscal_year_end":"12-31"}`))
	expectStatus(t, do(t, h, "POST", "/api/v1/salespeople", `{"org_id":`+o+`,"name":"Amy"}`, other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/salespeople", `{"org_id":`+o+`,"name":" "}`, owner), 400)
	id := int64(expectStatus(t, do(t, h, "POST", "/api/v1/salespeople", `{"org_id":`+o+`,"name":"Amy"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/salespeople", `{"org_id":`+o+`,"name":"amy"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/salespeople/"+itoa(id)+"/active", `{"org_id":`+o+`,"active":false}`, owner), 200)
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/salespeople?org_id="+o, "", owner), 200)["salespeople"].([]any); len(l) != 1 || l[0].(map[string]any)["active"] != false {
		t.Fatal(l)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-terms", `{"org_id":`+o+`,"name":"Net 30","days":30}`, owner), 409) // built in
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-terms", `{"org_id":`+o+`,"name":"Net 90","days":400}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-terms", `{"org_id":`+o+`,"name":"Net 90","days":90}`, owner), 201)
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/payment-terms?org_id="+o, "", owner), 200)["payment_terms"].([]any); len(l) != 6 {
		t.Fatal(len(l))
	}
}

func TestCustomPaymentModes(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "modes-owner@example.com")
	other := seedSession(t, a, "modes-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Modes Co","fiscal_year_end":"12-31"}`)
	otherOrg := orgFromList(t, a, other, `{"name":"Modes Other","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Walk-in"}`, owner), 201)))
	rev := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)))
	cash := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1015","name":"Till","type":"asset"}`, owner), 201)))
	receipt := func(mode, key string, status int) {
		t.Helper()
		expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", `{"org_id":`+o+`,"contact_id":`+cust+`,"receipt_date":"2026-10-15","payment_mode":"`+mode+`","deposit_account_id":`+cash+`,"idempotency_key":"`+key+`","lines":[{"description":"Item","account_id":`+rev+`,"quantity":1,"unit_price_cents":1000}]}`, owner), status)
	}
	receipt("GrabPay", "mode-key-001", 400) // unknown until defined
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-modes", `{"org_id":`+o+`,"name":"GrabPay"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-modes", `{"org_id":`+o+`,"name":"grabpay"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-modes", `{"org_id":`+o+`,"name":"PayNow"}`, owner), 409) // built-in
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-modes", `{"org_id":`+o+`,"name":"x"}`, other), 403)
	_ = otherOrg
	receipt("GrabPay", "mode-key-001", 201)
	receipt("cash", "mode-key-002", 201)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/payment-modes?org_id="+o, "", owner), 200)["payment_modes"].([]any)
	if len(list) != 7 {
		t.Fatal(len(list))
	}
	grab := itoa(int64(list[6].(map[string]any)["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-modes/"+grab+"/active", `{"org_id":`+o+`,"active":false}`, owner), 200)
	receipt("GrabPay", "mode-key-003", 400) // switched off
}

func TestSalesRoundingSetting(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "round-owner@example.com")
	other := seedSession(t, a, "round-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Round Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	if expectStatus(t, do(t, h, "GET", "/api/v1/sales-rounding?org_id="+o, "", owner), 200)["rounding"] != "none" {
		t.Fatal("default")
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/sales-rounding", `{"org_id":`+o+`,"rounding":"7c"}`, owner), 400)
	expectStatus(t, do(t, h, "PUT", "/api/v1/sales-rounding", `{"org_id":`+o+`,"rounding":"5c"}`, other), 403)
	if expectStatus(t, do(t, h, "PUT", "/api/v1/sales-rounding", `{"org_id":`+o+`,"rounding":"5c"}`, owner), 200)["rounding"] != "5c" {
		t.Fatal("set")
	}
	if expectStatus(t, do(t, h, "GET", "/api/v1/sales-rounding?org_id="+o, "", owner), 200)["rounding"] != "5c" {
		t.Fatal("read back")
	}
}

func TestPaymentDrafts(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "pay-draft@example.com")
	viewer := seedSession(t, a, "pay-draft-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Drafts","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='pay-draft-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	o := `"org_id":` + itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{`+o+`,"side":"received","data":{"amount":"10"}}`, viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{`+o+`,"side":"bogus","data":{}}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{`+o+`,"side":"received","data":[1]}`, owner), 400)
	made := expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{`+o+`,"side":"received","data":{"form":{"amount":"10.00"}}}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/payment-drafts", `{`+o+`,"side":"made","data":{"form":{}}}`, owner), 201)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/payment-drafts?org_id="+itoa(org)+"&side=received", "", viewer), 200)
	if len(got["drafts"].([]any)) != 1 {
		t.Fatalf("drafts=%v", got)
	}
	var journals int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil || journals != 0 {
		t.Fatalf("a draft must book nothing: journals=%d err=%v", journals, err)
	}
	url := "/api/v1/payment-drafts/" + itoa(int64(made["id"].(float64))) + "?org_id=" + itoa(org)
	update := `{` + o + `,"side":"received","data":{"form":{"amount":"20.00"}}}`
	expectStatus(t, do(t, h, "PUT", url, update, viewer), 403)
	expectStatus(t, do(t, h, "PUT", url, update, owner), 200)
	updated := expectStatus(t, do(t, h, "GET", "/api/v1/payment-drafts?org_id="+itoa(org)+"&side=received", "", owner), 200)
	form := updated["drafts"].([]any)[0].(map[string]any)["data"].(map[string]any)["form"].(map[string]any)
	if form["amount"] != "20.00" {
		t.Fatalf("draft not updated: %v", form)
	}
	expectStatus(t, do(t, h, "DELETE", url, "", viewer), 403)
	if r := do(t, h, "DELETE", url, "", owner); r.Code != 204 {
		t.Fatal(r.Code)
	}
	if r := do(t, h, "DELETE", url, "", owner); r.Code != 404 {
		t.Fatal(r.Code)
	}
}
