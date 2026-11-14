package main

import (
	"context"
	"strings"
	"testing"
)

func TestBankStatementImportAndMatch(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "bank-owner@example.com")
	viewer := seedSession(t, a, "bank-viewer@example.com")
	outsider := seedSession(t, a, "bank-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='bank-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"both","name":"Bank partner"}`, owner), 201)
	cid := int64(c["id"].(float64))
	// One posted invoice with a payment of 300.00 on 2026-11-05.
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-11-01","due_date":"2026-12-01","lines":[{"description":"Work","quantity":1,"unit_price_cents":30000}]}`, owner), 201)
	invID := int64(inv["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+itoa(org), "", owner), 200)
	pay := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/payments", `{"org_id":`+itoa(org)+`,"amount_cents":30000,"payment_date":"2026-11-05","idempotency_key":"pay-1"}`, owner), 201)
	payID := int64(pay["payment_id"].(float64))
	// One posted bill with a payment of 120.00 on 2026-11-06.
	sup := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Supplier"}`, owner), 201)
	sid := int64(sup["id"].(float64))
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6300","name":"Services","type":"expense"}`, owner), 201)
	aid := int64(acct["id"].(float64))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(sid)+`,"supplier_reference":"B-1","bill_date":"2026-11-01","due_date":"2026-12-01","lines":[{"account_id":`+itoa(aid)+`,"description":"Service","quantity":1,"unit_price_cents":12000}]}`, owner), 201)
	bid := int64(bill["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bid)+"/post?org_id="+itoa(org), "", owner), 200)
	bp := expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bid)+"/payments", `{"org_id":`+itoa(org)+`,"amount_cents":12000,"payment_date":"2026-11-06","idempotency_key":"bp-1"}`, owner), 201)
	bpID := int64(bp["payment_id"].(float64))

	csv := "date,description,reference,amount\n2026-11-05,Customer receipt,INV1,300.00\n2026-11-06,Supplier transfer,B1,-120.00\n2026-11-07,Bank fee,FEE,-5.50\n"
	rec := do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), csv, viewer)
	if rec.Code != 403 {
		t.Fatal("viewer imported", rec.Code)
	}
	rec = do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), csv, owner)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"imported":3`) {
		t.Fatal(rec.Body.String())
	}
	// Re-importing the same file adds nothing.
	rec = do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), csv, owner)
	if !strings.Contains(rec.Body.String(), `"imported":0`) || !strings.Contains(rec.Body.String(), `"duplicates_skipped":3`) {
		t.Fatal("reimport not deduplicated", rec.Body.String())
	}
	if rec := do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), "date,description,reference,amount\n2026-13-99,x,,1.00\n", owner); rec.Code != 400 {
		t.Fatal("bad date accepted", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/bank/statements?org_id="+itoa(org), "date,description,reference,amount\n2026-11-05,x,,1.001\n", owner); rec.Code != 400 {
		t.Fatal("three decimals accepted", rec.Code)
	}

	lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+itoa(org), "", viewer), 200)["lines"].([]any)
	if len(lines) != 3 {
		t.Fatal(lines)
	}
	var receiptID, billLineID, feeID int64
	for _, raw := range lines {
		l := raw.(map[string]any)
		id := int64(l["id"].(float64))
		switch l["amount_cents"] {
		case float64(30000):
			receiptID = id
		case float64(-12000):
			billLineID = id
		case float64(-550):
			feeID = id
		}
	}
	if receiptID == 0 || billLineID == 0 || feeID == 0 {
		t.Fatal("expected three lines", lines)
	}
	match := func(lineID int64, kind string, payment int64, token string) int {
		body := `{"org_id":` + itoa(org) + `,"kind":"` + kind + `","payment_id":` + itoa(payment) + `}`
		return do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/match", body, token).Code
	}
	if code := match(receiptID, "invoice", payID, viewer); code != 403 {
		t.Fatal("viewer matched", code)
	}
	if code := match(receiptID, "bill", bpID, owner); code != 422 {
		t.Fatal("money-in line matched to bill", code)
	}
	if code := match(billLineID, "invoice", payID, owner); code != 422 {
		t.Fatal("money-out line matched to invoice", code)
	}
	if code := match(feeID, "bill", bpID, owner); code != 422 {
		t.Fatal("mismatched amount accepted", code)
	}
	if code := match(receiptID, "invoice", payID, owner); code != 200 {
		t.Fatal("valid receipt match refused", code)
	}
	if code := match(receiptID, "invoice", payID, owner); code != 409 {
		t.Fatal("line rematch allowed", code)
	}
	if code := match(billLineID, "bill", bpID, owner); code != 200 {
		t.Fatal("valid bill match refused", code)
	}
	// A payment cannot be matched to two lines.
	if code := match(feeID, "invoice", payID, owner); code != 422 && code != 409 {
		t.Fatal("payment matched twice", code)
	}
	if _, err := pool.Exec(ctx, `UPDATE bank_statement_lines SET amount_cents=1 WHERE org_id=$1`, org); err == nil {
		t.Fatal("imported line mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM bank_statement_lines WHERE org_id=$1`, org); err == nil {
		t.Fatal("imported line deletable")
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+itoa(org), "", outsider), 403)
}
