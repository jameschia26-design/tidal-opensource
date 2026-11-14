package main

import "testing"

// Contact financial details round trip, plus the invoice credit-limit block.
func TestContactFinancialDetailsAndCreditLimit(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "limit-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Limit co","fiscal_year_end":"12-31"}`)
	acctList := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+itoa(org), "", owner), 200)["accounts"].([]any)
	var salesID, expenseID int64
	for _, x := range acctList {
		m := x.(map[string]any)
		switch m["type"] {
		case "revenue":
			if salesID == 0 {
				salesID = int64(m["id"].(float64))
			}
		case "expense":
			if expenseID == 0 {
				expenseID = int64(m["id"].(float64))
			}
		}
	}
	exp := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Office costs","type":"expense"}`, owner), 201)
	expenseID = int64(exp["id"].(float64))
	body := `{"org_id":` + itoa(org) + `,"role":"customer","name":"Limit Buyer","fax":"+65 6123 4567","direct_dial":"+65 6123 0000","country":"SG",
		"bank_account_name":"Limit Buyer Pte Ltd","bank_account_number":"123-456-789","bank_particulars":"INV","credit_limit_cents":50000,"block_over_limit":true,
		"sales_account_id":` + itoa(salesID) + `,"purchase_account_id":` + itoa(expenseID) + `}`
	created := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", body, owner), 201)
	contactID := int64(created["id"].(float64))
	list := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any)
	c := list[0].(map[string]any)
	if c["fax"] != "+65 6123 4567" || c["bank_account_number"] != "123-456-789" || c["credit_limit_cents"].(float64) != 50000 ||
		c["block_over_limit"] != true || int64(c["sales_account_id"].(float64)) != salesID || int64(c["purchase_account_id"].(float64)) != expenseID {
		t.Fatal(c)
	}
	// A wrong-typed default account is refused.
	if rec := do(t, h, "PUT", "/api/v1/contacts/"+itoa(contactID), `{"org_id":`+itoa(org)+`,"role":"customer","name":"Limit Buyer","sales_account_id":`+itoa(expenseID)+`}`, owner); rec.Code != 422 {
		t.Fatal("expense account as sales default", rec.Code)
	}
	// Blocking without a limit is refused.
	if rec := do(t, h, "PUT", "/api/v1/contacts/"+itoa(contactID), `{"org_id":`+itoa(org)+`,"role":"customer","name":"Limit Buyer","credit_limit_cents":0,"block_over_limit":true}`, owner); rec.Code != 400 {
		t.Fatal("block without limit", rec.Code)
	}
	invoice := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contactID) + `,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":60000}]}`
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice, owner), 201)
	invID := int64(inv["id"].(float64))
	if rec := do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+itoa(org), "", owner); rec.Code != 409 {
		t.Fatalf("post over limit = %d, want 409", rec.Code)
	}
	// A second invoice that stays under the limit still posts.
	small := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contactID) + `,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Small","quantity":1,"unit_price_cents":40000}]}`
	inv2 := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", small, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv2["id"].(float64)))+"/post?org_id="+itoa(org), "", owner), 200)
	// With 40,000 used, the first invoice would take it to 100,000: still over.
	if rec := do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+itoa(org), "", owner); rec.Code != 409 {
		t.Fatal("second post over limit", rec.Code)
	}
}
