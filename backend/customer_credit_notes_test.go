package main

import "testing"

func TestCustomerCreditNotesDraftOpenVoid(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ccn-owner@example.com")
	other := seedSession(t, a, "ccn-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Credits","fiscal_year_end":"12-31"}`)
	orgOther := orgFromList(t, a, other, `{"name":"Credits other","fiscal_year_end":"12-31"}`)
	_ = orgOther
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Buyer"}`, owner), 201))
	sup := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Seller"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"4100","name":"Services","type":"revenue"}`, owner), 201))
	exp := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Cost","type":"expense"}`, owner), 201))
	body := func(contact, account int64, extra string) string {
		return `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(contact) + `,"credit_date":"2026-10-15","lines":[{"description":"Refund","account_id":` + itoa(account) + `,"quantity":2,"unit_price_cents":5000}]` + extra + `}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(sup, rev, ""), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(cust, exp, ""), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(cust, rev, ""), other), 403)
	// 10000 subtotal - 1000 discount + 500 shipping = 9500
	made := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(cust, rev, `,"commercial":{"discount_type":"flat","discount_cents":1000,"shipping_cents":500}`), owner), 201)
	if made["total_cents"] != float64(9500) || made["credit_number"] != "CN-00001" {
		t.Fatal(made)
	}
	cid := id(made)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(cust, rev, `,"credit_number":"CN-00001"`), owner), 409)
	expectStatus(t, do(t, h, "PUT", "/api/v1/credit-notes/"+itoa(cid), body(cust, rev, `,"commercial":{"discount_type":"flat","discount_cents":1000,"shipping_cents":500}`), owner), 200)
	path := "/api/v1/credit-notes/" + itoa(cid) + "/stage"
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"stage":"open"}`, other), 403)
	opened := expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 200)
	if opened["status"] != "open" || opened["journal_id"] == nil {
		t.Fatal(opened)
	}
	// replay is a no-op, edits and voids are refused once open
	again := expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 200)
	if again["journal_id"] != opened["journal_id"] {
		t.Fatal("replay posted again")
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/credit-notes/"+itoa(cid), body(cust, rev, ""), owner), 409)
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`,"stage":"void"}`, owner), 409)
	var ar, revenue int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(jl.credit_cents-jl.debit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='1100'`, org).Scan(&ar); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(jl.debit_cents-jl.credit_cents),0) FROM journal_lines jl WHERE jl.org_id=$1 AND jl.account_id=$2`, org, rev).Scan(&revenue); err != nil {
		t.Fatal(err)
	}
	if ar != 9500 || revenue != 9500 {
		t.Fatalf("ar credit %d revenue debit %d", ar, revenue)
	}
	// a draft can be voided and then cannot post
	d := id(expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body(cust, rev, ""), owner), 201))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+itoa(d)+"/stage", `{"org_id":`+itoa(org)+`,"stage":"void"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+itoa(d)+"/stage", `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 409)
	if listed := expectStatus(t, do(t, h, "GET", "/api/v1/credit-notes?org_id="+itoa(org), "", owner), 200)["credit_notes"].([]any); len(listed) != 2 {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/credit-notes/"+itoa(cid)+"/attachments?org_id="+itoa(org), "", owner), 200)
}

func TestCustomerCreditNoteGst(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ccn-gst@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST credits","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Buyer"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"4100","name":"Services","type":"revenue"}`, owner), 201))
	body := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cust) + `,"credit_date":"2026-03-10","lines":[{"description":"Refund","account_id":` + itoa(rev) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	made := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", body, owner), 201)
	if made["total_cents"] != float64(10900) {
		t.Fatal(made)
	}
	cid := id(made)
	opened := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+itoa(cid)+"/stage", `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 200)
	if opened["status"] != "open" {
		t.Fatal(opened)
	}
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
		t.Fatalf("gst output=%d ar=%d revenue=%d, want 900/10900/10000", output, ar, revenue)
	}
	// Unregistered companies keep the old refusal.
	plain := seedSession(t, a, "ccn-plain@example.com")
	porg := orgFromList(t, a, plain, `{"name":"Plain credits","fiscal_year_end":"12-31"}`)
	pcust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(porg)+`,"role":"customer","name":"Buyer"}`, plain), 201)["id"].(float64))
	prev := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(porg)+`,"code":"4100","name":"Services","type":"revenue"}`, plain), 201)["id"].(float64))
	bad := `{"org_id":` + itoa(porg) + `,"contact_id":` + itoa(pcust) + `,"credit_date":"2026-03-10","lines":[{"description":"Refund","account_id":` + itoa(prev) + `,"quantity":1,"unit_price_cents":1000,"gst_code":"SR"}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", bad, plain), 422)
}

func TestCustomerCreditNoteProjectLine(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ccn-project@example.com")
	org := orgFromList(t, a, owner, `{"name":"Project credits","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201))
	rev := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Services","type":"revenue"}`, owner), 201))
	proj := id(expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"name":"Fit-out"}`, owner), 201))
	made := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"credit_date":"2026-10-15","lines":[{"description":"Refund","account_id":`+itoa(rev)+`,"quantity":1,"unit_price_cents":5000,"project_id":`+itoa(proj)+`}]}`, owner), 201)
	var stored int64
	if err := pool.QueryRow(t.Context(), `SELECT (lines->0->>'project_id')::bigint FROM customer_credit_notes WHERE org_id=$1 AND id=$2`, org, id(made)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != proj {
		t.Fatalf("project_id = %d, want %d", stored, proj)
	}
}
