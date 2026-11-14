package main

import "testing"

// Phase 2a server rules: archived contacts stay out of pickers, group membership round-trips,
// a contact discount is applied to new invoice lines, and a tax-inclusive credit note splits the GST.

func TestArchivedContactHiddenFromPickers(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "archive-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Archive co","fiscal_year_end":"12-31"}`)
	active := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Active buyer"}`, owner), 201)["id"].(float64))
	gone := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Gone buyer"}`, owner), 201)["id"].(float64))

	expectStatus(t, do(t, h, "POST", "/api/v1/contacts/"+itoa(gone)+"/archive", `{"org_id":`+itoa(org)+`,"archived":true}`, owner), 200)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org)+"&role=customer", "", owner), 200)["contacts"].([]any)
	if len(list) != 1 || int64(list[0].(map[string]any)["id"].(float64)) != active {
		t.Fatalf("archived contact visible to a picker: %v", list)
	}
	archived := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org)+"&role=customer&archived=1", "", owner), 200)["contacts"].([]any)
	if len(archived) != 1 || int64(archived[0].(map[string]any)["id"].(float64)) != gone {
		t.Fatalf("archived tab missing the contact: %v", archived)
	}
	// Restoring puts it back in the picker; the document history was never touched.
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts/"+itoa(gone)+"/archive", `{"org_id":`+itoa(org)+`,"archived":false}`, owner), 200)
	list = expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org)+"&role=customer", "", owner), 200)["contacts"].([]any)
	if len(list) != 2 {
		t.Fatalf("restored contact hidden: %v", list)
	}
}

func TestContactGroupsAndSmartLists(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "groups-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Group co","fiscal_year_end":"12-31"}`)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	one := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"One"}`, owner), 201))
	two := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Two"}`, owner), 201))

	group := id(expectStatus(t, do(t, h, "POST", "/api/v1/contact-groups", `{"org_id":`+itoa(org)+`,"name":"Key accounts"}`, owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/contact-groups", `{"org_id":`+itoa(org)+`,"name":"key ACCOUNTS"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/contact-groups/"+itoa(group)+"/members", `{"org_id":`+itoa(org)+`,"contact_ids":[`+itoa(one)+`,`+itoa(two)+`]}`, owner), 200)
	// Adding the same contact again is a no-op, not a duplicate row.
	expectStatus(t, do(t, h, "POST", "/api/v1/contact-groups/"+itoa(group)+"/members", `{"org_id":`+itoa(org)+`,"contact_ids":[`+itoa(one)+`]}`, owner), 200)
	groups := expectStatus(t, do(t, h, "GET", "/api/v1/contact-groups?org_id="+itoa(org), "", owner), 200)["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups: %v", groups)
	}
	members := groups[0].(map[string]any)["contact_ids"].([]any)
	if len(members) != 2 {
		t.Fatalf("members: %v", members)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/contact-groups/"+itoa(group), `{"org_id":`+itoa(org)+`,"name":"Top accounts"}`, owner), 200)
	groups = expectStatus(t, do(t, h, "GET", "/api/v1/contact-groups?org_id="+itoa(org), "", owner), 200)["groups"].([]any)
	if groups[0].(map[string]any)["name"] != "Top accounts" {
		t.Fatal(groups)
	}
	expectStatus(t, do(t, h, "DELETE", "/api/v1/contact-groups/"+itoa(group)+"/members/"+itoa(one)+"?org_id="+itoa(org), "", owner), 200)
	groups = expectStatus(t, do(t, h, "GET", "/api/v1/contact-groups?org_id="+itoa(org), "", owner), 200)["groups"].([]any)
	if n := len(groups[0].(map[string]any)["contact_ids"].([]any)); n != 1 {
		t.Fatalf("member not removed: %v", groups)
	}

	list := id(expectStatus(t, do(t, h, "POST", "/api/v1/contact-smart-lists", `{"org_id":`+itoa(org)+`,"name":"SG buyers","query":"singapore"}`, owner), 201))
	if saved := expectStatus(t, do(t, h, "GET", "/api/v1/contact-smart-lists?org_id="+itoa(org), "", owner), 200)["smart_lists"].([]any); len(saved) != 1 {
		t.Fatalf("smart lists: %v", saved)
	}
	expectStatus(t, do(t, h, "DELETE", "/api/v1/contact-smart-lists/"+itoa(list)+"?org_id="+itoa(org), "", owner), 200)
	if saved := expectStatus(t, do(t, h, "GET", "/api/v1/contact-smart-lists?org_id="+itoa(org), "", owner), 200)["smart_lists"].([]any); len(saved) != 0 {
		t.Fatal(saved)
	}
}

func TestContactDiscountAndSeparateBillTerms(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "discount-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Discount co","fiscal_year_end":"12-31"}`)
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Ten percent","payment_terms_days":30,"bill_payment_terms_days":7,"discount_basis_points":1000}`, owner), 201)
	contactID := int64(contact["id"].(float64))
	if contact["discount_basis_points"].(float64) != 1000 || contact["bill_payment_terms_days"].(float64) != 7 {
		t.Fatalf("contact defaults: %v", contact)
	}
	// A line with no discount of its own takes the contact's 10%.
	invoice := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contactID) + `,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`
	made := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", invoice, owner), 201)
	if made["total_cents"].(float64) != 9000 {
		t.Fatalf("contact discount not applied: %v", made)
	}
	// A discount typed on the line wins over the contact default.
	own := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contactID) + `,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000,"discount_basis_points":2500}]}`
	if made := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", own, owner), 201); made["total_cents"].(float64) != 7500 {
		t.Fatalf("line discount overridden: %v", made)
	}
}

func TestCreditNoteTaxInclusive(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ccn-inclusive@example.com")
	org := orgFromList(t, a, owner, `{"name":"Inclusive credits","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Buyer"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"4100","name":"Services","type":"revenue"}`, owner), 201))
	// 109.00 typed as tax inclusive is 100.00 net + 9.00 GST, not 109.00 + GST.
	body := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cust) + `,"credit_date":"2026-03-10","amounts_are":"inclusive","lines":[{"description":"Refund","account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10900,"gst_code":"SR"}]}`
	made := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body, owner), 201)
	if made["total_cents"].(float64) != 10900 {
		t.Fatalf("inclusive total: %v", made)
	}
	opened := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+itoa(id(made))+"/stage", `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 200)
	var output, ar, revenue int64
	if err := pool.QueryRow(t.Context(), `SELECT
		COALESCE(sum(jl.debit_cents) FILTER (WHERE a.code='2300'),0),
		COALESCE(sum(jl.credit_cents) FILTER (WHERE a.code='1100'),0),
		COALESCE(sum(jl.debit_cents) FILTER (WHERE a.code='4100'),0)
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.id=$2`, org, int64(opened["journal_id"].(float64))).Scan(&output, &ar, &revenue); err != nil {
		t.Fatal(err)
	}
	if output != 900 || ar != 10900 || revenue != 10000 {
		t.Fatalf("inclusive gst output=%d ar=%d revenue=%d, want 900/10900/10000", output, ar, revenue)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/credit-notes?org_id="+itoa(org), "", owner), 200)["credit_notes"].([]any)
	if listed[0].(map[string]any)["amounts_are"] != "inclusive" {
		t.Fatalf("amounts_are not stored: %v", listed[0])
	}
}
