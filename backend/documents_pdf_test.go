package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestDocumentPDFAccessAndContent(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "pdf-owner@example.com")
	outsider := seedSession(t, a, "pdf-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"PDF Seller Pte Ltd","fiscal_year_end":"12-31"}`)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Acme (Test)","company_name":"Acme Pte Ltd"}`, owner), 201)
	cid := int64(c["id"].(float64))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"invoice_date":"2026-10-29","due_date":"2026-11-28","notes":"Thank you\nsecond line","lines":[{"description":"Consulting \\ hours","quantity":3,"unit_price_cents":10000}]}`, owner), 201)
	invID := int64(inv["id"].(float64))
	rec := do(t, h, "GET", "/api/v1/documents/invoices/"+itoa(invID)+"/pdf?org_id="+itoa(org), "", owner)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Fatal(rec.Code, rec.Header())
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("%PDF-1.4")) || !bytes.Contains(body, []byte("DRAFT")) || !bytes.Contains(body, []byte("Total SGD 300.00")) {
		t.Fatal("draft invoice PDF missing expected content")
	}
	if !bytes.Contains(body, []byte(`Consulting \\ hours`)) {
		t.Fatal("line text not escaped in PDF")
	}
	if rec := do(t, h, "GET", "/api/v1/documents/invoices/"+itoa(invID)+"/pdf?org_id="+itoa(org), "", outsider); rec.Code != 403 {
		t.Fatal("outsider read PDF", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/v1/documents/invoices/999999/pdf?org_id="+itoa(org), "", owner); rec.Code != 404 {
		t.Fatal("missing invoice", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/v1/documents/unknown/"+itoa(invID)+"/pdf?org_id="+itoa(org), "", owner); rec.Code != 404 {
		t.Fatal("unknown kind", rec.Code)
	}
	quote := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"quote_date":"2026-10-29","expiry_date":"2026-11-29","commercial":{"salesperson":"PDF Test","order_number":"ORDER-7"},"lines":[{"description":"Setup","quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	qid := int64(quote["id"].(float64))
	if rec := do(t, h, "GET", "/api/v1/documents/quotes/"+itoa(qid)+"/pdf?org_id="+itoa(org), "", owner); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Quotation") {
		t.Fatal("quote PDF", rec.Code)
	}
	for kind, id := range map[string]int64{"invoices": invID, "quotes": qid} {
		path := "/api/v1/documents/" + kind + "/" + itoa(id) + "/pdf?org_id=" + itoa(org)
		standard := do(t, h, "GET", path+"&template=standard", "", owner)
		detailed := do(t, h, "GET", path+"&template=detailed", "", owner)
		if standard.Code != 200 || detailed.Code != 200 || strings.Contains(standard.Body.String(), "Salesperson:") || !strings.Contains(detailed.Body.String(), "Salesperson:") {
			t.Fatalf("%s template selection failed: standard=%d detailed=%d", kind, standard.Code, detailed.Code)
		}
		if kind == "quotes" && !strings.Contains(detailed.Body.String(), "ORDER-7") {
			t.Fatal("quote detailed template missing order number")
		}
		if invalid := do(t, h, "GET", path+"&template=unknown", "", owner); invalid.Code != 400 {
			t.Fatalf("%s accepted invalid template: %d", kind, invalid.Code)
		}
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+itoa(org), "", owner), 200)
	cn := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/credit-notes", `{"org_id":`+itoa(org)+`,"credit_date":"2026-10-29","reason":"Returned","amount_cents":1000}`, owner), 201)
	cnID := int64(cn["id"].(float64))
	if rec := do(t, h, "GET", "/api/v1/documents/credit-notes/"+itoa(cnID)+"/pdf?org_id="+itoa(org), "", owner); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Credit note") {
		t.Fatal("credit note PDF", rec.Code)
	}
	if _, err := pool.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentPDFNewKindsAndTemplate(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "pdf2-owner@example.com")
	outsider := seedSession(t, a, "pdf2-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"PDF Kinds Pte Ltd","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	cust := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)))
	sup := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Seller"}`, owner), 201)))
	rev := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)))
	exp := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6400","name":"Supplies","type":"expense"}`, owner), 201)))
	cash := itoa(id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1015","name":"Petty cash","type":"asset"}`, owner), 201)))
	com := `,"commercial":{"salesperson":"Alice","order_number":"ORD-7","payment_terms_days":30,"discount_type":"flat","discount_cents":500,"shipping_cents":200}`
	line := func(acct string) string {
		return `"lines":[{"description":"Widget","account_id":` + acct + `,"quantity":3,"unit_price_cents":1000}]`
	}
	po := id(expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", `{"org_id":`+o+`,"contact_id":`+sup+`,"po_date":"2026-10-29","delivery_date":"2026-11-05",`+line(exp)+com+`}`, owner), 201))
	sr := id(expectStatus(t, do(t, h, "POST", "/api/v1/sales-receipts", `{"org_id":`+o+`,"contact_id":`+cust+`,"receipt_date":"2026-10-29","payment_mode":"cash","deposit_account_id":`+cash+`,"idempotency_key":"pdf-key-001",`+line(rev)+com+`}`, owner), 201))
	cn := id(expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+cust+`,"credit_date":"2026-10-29",`+line(rev)+com+`}`, owner), 201))
	for kind, docID := range map[string]int64{"purchase-orders": po, "sales-receipts": sr, "customer-credit-notes": cn} {
		path := "/api/v1/documents/" + kind + "/" + itoa(docID) + "/pdf?org_id=" + o
		std := do(t, h, "GET", path, "", owner)
		if std.Code != 200 || !strings.Contains(std.Body.String(), "Total SGD 27.00") || strings.Contains(std.Body.String(), "Salesperson") {
			t.Fatal(kind, "standard", std.Code)
		}
		det := do(t, h, "GET", path+"&template=detailed", "", owner)
		for _, want := range []string{"Salesperson: Alice", "Order number: ORD-7", "Payment terms: 30 days", "Discount: SGD 5.00", "Shipping: SGD 2.00"} {
			if det.Code != 200 || !strings.Contains(det.Body.String(), want) {
				t.Fatal(kind, "detailed missing", want, det.Code)
			}
		}
		if rec := do(t, h, "GET", path+"&template=fancy", "", owner); rec.Code != 400 {
			t.Fatal(kind, "bad template", rec.Code)
		}
		if rec := do(t, h, "GET", path, "", outsider); rec.Code != 403 {
			t.Fatal(kind, "outsider", rec.Code)
		}
		if rec := do(t, h, "GET", "/api/v1/documents/"+kind+"/999999/pdf?org_id="+o, "", owner); rec.Code != 404 {
			t.Fatal(kind, "missing", rec.Code)
		}
	}
	bill := id(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+sup+`,"supplier_reference":"SUP-9","bill_date":"2026-10-29","due_date":"2026-11-29",`+line(exp)+`}`, owner), 201))
	bp := "/api/v1/documents/bills/" + itoa(bill) + "/pdf?org_id=" + o
	if rec := do(t, h, "GET", bp, "", owner); rec.Code != 200 || strings.Contains(rec.Body.String(), "Supplier reference") {
		t.Fatal("bill standard", rec.Code)
	}
	if rec := do(t, h, "GET", bp+"&template=detailed", "", owner); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Supplier reference: SUP-9") || !strings.Contains(rec.Body.String(), "Status: draft") {
		t.Fatal("bill detailed", rec.Code)
	}
	if rec := do(t, h, "GET", bp+"&template=fancy", "", owner); rec.Code != 400 {
		t.Fatal("bill bad template", rec.Code)
	}
}
