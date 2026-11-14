package main

import (
	"strings"
	"testing"
)

func TestCustomerStatement(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "statement-owner@example.com")
	outsider := seedSession(t, a, "statement-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Statement co","fiscal_year_end":"12-31"}`)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := int64(c["id"].(float64))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-01-01","due_date":"2026-02-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)
	iid := int64(inv["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(iid)+"/post?org_id="+itoa(org), "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(iid)+"/payments", `{"org_id":`+itoa(org)+`,"amount_cents":3000,"payment_date":"2026-02-10","idempotency_key":"statement-pay"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(iid)+"/credit-notes", `{"org_id":`+itoa(org)+`,"credit_date":"2026-02-12","reason":"Correction","amount_cents":1000}`, owner), 201)
	expectStatus(t, do(t, h, "PUT", "/api/v1/contacts/"+itoa(cid), `{"org_id":`+itoa(org)+`,"role":"customer","name":"Customer","opening_balance_cents":1000,"opening_balance_date":"2026-01-15"}`, owner), 200)
	url := "/api/v1/statements?org_id=" + itoa(org) + "&contact_id=" + itoa(cid) + "&from=2026-02-01&to=2026-02-28"
	expectStatus(t, do(t, h, "GET", url, "", outsider), 403)
	result := expectStatus(t, do(t, h, "GET", url, "", owner), 200)
	if result["brought_forward_cents"] != float64(11000) || result["balance_cents"] != float64(7000) || len(result["rows"].([]any)) != 2 {
		t.Fatal(result)
	}
	rev := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)
	credit := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"credit_date":"2026-02-14","lines":[{"description":"Return","account_id":`+itoa(int64(rev["id"].(float64)))+`,"quantity":1,"unit_price_cents":2000}]}`, owner), 201)
	creditID := itoa(int64(credit["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+creditID+"/stage", `{"org_id":`+itoa(org)+`,"stage":"open"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/customer-credit-notes/"+creditID+"/apply", `{"org_id":`+itoa(org)+`,"invoice_id":`+itoa(iid)+`,"amount_cents":500,"applied_on":"2026-02-15"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/customer-credit-notes/"+creditID+"/refund", `{"org_id":`+itoa(org)+`,"amount_cents":1000,"refund_date":"2026-02-16","idempotency_key":"statement-refund"}`, owner), 201)
	result = expectStatus(t, do(t, h, "GET", url, "", owner), 200)
	if result["balance_cents"] != float64(6000) || len(result["rows"].([]any)) != 4 {
		t.Fatalf("credit and refund must appear once; application is not a movement: %v", result)
	}
	beforeRefund := expectStatus(t, do(t, h, "GET", "/api/v1/statements?org_id="+itoa(org)+"&contact_id="+itoa(cid)+"&from=2026-02-01&to=2026-02-15", "", owner), 200)
	if beforeRefund["balance_cents"] != float64(5000) {
		t.Fatalf("future refund must not appear: %v", beforeRefund)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/statements?org_id="+itoa(org)+"&contact_id="+itoa(cid)+"&from=2026-03-01&to=2026-02-01", "", owner), 400)
}

func TestEmailStatement(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	var sentTo, sentBody string
	a.mail.sink = func(to, subject, body string) error { sentTo, sentBody = to, body; return nil }
	owner := seedSession(t, a, "statement-mail@example.com")
	viewer := seedSession(t, a, "statement-mail-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Mail co","fiscal_year_end":"12-31"}`)
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='statement-mail-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"name":"Mail Co Customer","role":"customer","email":"customer@example.com"}`, owner), 201)
	cid := int64(contact["id"].(float64))
	// An invoice gives the statement something to print.
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Consulting","quantity":1,"unit_price_cents":50000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+itoa(org), "", owner), 200)
	body := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cid) + `,"from":"2026-03-01","to":"2026-03-31"}`
	expectStatus(t, do(t, h, "POST", "/api/v1/statements/send", body, viewer), 403)
	m := expectStatus(t, do(t, h, "POST", "/api/v1/statements/send", body, owner), 200)
	if m["sent_to"] != "customer@example.com" || sentTo != "customer@example.com" {
		t.Fatal(m, sentTo)
	}
	if !strings.Contains(sentBody, "Mail Co Customer statement") || !strings.Contains(sentBody, "Invoice") || !strings.Contains(sentBody, "500.00") {
		t.Fatalf("unexpected body: %s", sentBody)
	}
	// The table of customer balances agrees with the statement opened above.
	sum := expectStatus(t, do(t, h, "GET", "/api/v1/statements/summary?org_id="+itoa(org)+"&from=2026-03-01&to=2026-03-31", "", owner), 200)
	found := false
	for _, c := range sum["customers"].([]any) {
		row := c.(map[string]any)
		if int64(row["contact_id"].(float64)) == cid {
			found = true
			if int64(row["balance_cents"].(float64)) != 50000 || row["email"] != "customer@example.com" {
				t.Fatalf("summary row %v", row)
			}
		}
	}
	if !found {
		t.Fatal("customer missing from summary", sum)
	}
	// A customer without an email is refused, not silently skipped.
	noMail := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"name":"No Mail","role":"customer"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/statements/send", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(int64(noMail["id"].(float64)))+`,"from":"2026-03-01","to":"2026-03-31"}`, owner), 422)
	// Unconfigured mail answers 503 rather than pretending.
	a.mail = &mailer{}
	expectStatus(t, do(t, h, "POST", "/api/v1/statements/send", body, owner), 503)
}
