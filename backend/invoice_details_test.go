package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestInvoiceDraftEditingAndFrozenParties(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := t.Context()
	owner := seedSession(t, a, "invoice-owner@example.com")
	viewer := seedSession(t, a, "invoice-viewer@example.com")
	outsider := seedSession(t, a, "invoice-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Original seller","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	contact := seedContact(t, a, org, owner)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='invoice-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	input := invoiceIn{OrgID: org, ContactID: contact, InvoiceDate: "2026-10-01", DueDate: "2026-10-31", Reference: "PO-1", Notes: "Thank you", Terms: "Pay within 30 days", Lines: []invoiceLineIn{{Description: "Original", Quantity: 1, UnitPriceCents: 100}}}
	body := func() string {
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(), owner), 201)
	id := int64(created["id"].(float64))
	url := "/api/v1/invoices/" + coreID(id)
	query := "?org_id=" + coreID(org)
	input.Reference = "PO-2"
	input.Notes = "Updated notes\nSecond line"
	input.Terms = "Bank transfer"
	input.Lines = []invoiceLineIn{{Description: "New work", Quantity: 2, UnitPriceCents: 150}, {Description: "Materials", Quantity: 1, UnitPriceCents: 50}}
	expectStatus(t, do(t, h, "PUT", url, body(), viewer), 403)
	expectStatus(t, do(t, h, "PUT", url, body(), outsider), 403)
	expectStatus(t, do(t, h, "PUT", "/api/v1/invoices/999999", body(), owner), 404)
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 200)
	read := func() map[string]any { return expectStatus(t, do(t, h, "GET", url+query, "", owner), 200) }
	detail := read()
	invoice := detail["invoice"].(map[string]any)
	if invoice["reference"] != "PO-2" || invoice["notes"] != input.Notes || invoice["terms"] != input.Terms || invoice["total_cents"] != float64(350) || len(detail["lines"].([]any)) != 2 {
		t.Fatalf("draft did not roundtrip: %v", detail)
	}
	input.Notes = strings.Repeat("x", 4001)
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 400)
	input.Notes = "bad\x00notes"
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 400)
	input.Notes = "Updated notes\nSecond line"

	// Header-only changes and deleting all lines must not leave an unbalanced draft.
	for _, statement := range []string{
		`UPDATE invoices SET total_cents=1 WHERE id=$1`,
		`DELETE FROM invoice_lines WHERE invoice_id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, id); err == nil {
			t.Fatalf("invalid draft accepted: %s", statement)
		}
	}
	other := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(), owner), 201)
	if _, err := pool.Exec(ctx, `UPDATE invoice_lines SET invoice_id=$2,line_no=3 WHERE invoice_id=$1 AND line_no=1`, id, int64(other["id"].(float64))); err == nil {
		t.Fatal("line moved between invoices")
	}

	expectStatus(t, do(t, h, "POST", url+"/post"+query, "", owner), 200)
	posted := read()["invoice"].(map[string]any)
	if posted["seller_snapshot"].(map[string]any)["name"] != "Original seller" || posted["customer_snapshot"].(map[string]any)["name"] != "Buyer Pte" {
		t.Fatalf("missing frozen parties: %v", posted)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET name='Changed seller' WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE contacts SET name='Changed buyer' WHERE id=$1`, contact); err != nil {
		t.Fatal(err)
	}
	invoice = read()["invoice"].(map[string]any)
	before, _ := json.Marshal(posted)
	after, _ := json.Marshal(invoice)
	if string(before) != string(after) {
		t.Fatalf("posted invoice changed after profile edits: %s / %s", before, after)
	}
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 409)
	for _, statement := range []string{
		`UPDATE invoices SET notes='changed' WHERE id=$1`,
		`UPDATE invoice_lines SET description='changed' WHERE invoice_id=$1`,
		`DELETE FROM invoice_lines WHERE invoice_id=$1`,
		`INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents) SELECT org_id,id,3,'extra',1,0,0 FROM invoices WHERE id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, id); err == nil {
			t.Fatalf("posted mutation accepted: %s", statement)
		}
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='invoice.updated'`, org).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("update audits=%d err=%v", audits, err)
	}
}

func TestInvoiceEditAndPostSerialize(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "invoice-race@example.com")
	org := orgFromList(t, a, owner, `{"name":"Race seller","fiscal_year_end":"12-31"}`)
	contact := seedContact(t, a, org, owner)
	input := invoiceIn{OrgID: org, ContactID: contact, InvoiceDate: "2026-10-01", DueDate: "2026-10-31", Lines: []invoiceLineIn{{Description: "Work", Quantity: 1, UnitPriceCents: 100}}}
	b, _ := json.Marshal(input)
	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", string(b), owner), 201)
	id := int64(created["id"].(float64))
	url := "/api/v1/invoices/" + coreID(id)
	input.Lines[0].UnitPriceCents = 250
	b, _ = json.Marshal(input)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	wg.Add(2)
	go func() { defer wg.Done(); codes <- do(t, h, "PUT", url, string(b), owner).Code }()
	go func() { defer wg.Done(); codes <- do(t, h, "POST", url+"/post?org_id="+coreID(org), "", owner).Code }()
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 && code != 409 {
			t.Fatalf("edit/post race status %d", code)
		}
	}
	var total, debit int64
	if err := a.db.QueryRow(t.Context(), `SELECT i.total_cents,sum(l.debit_cents) FROM invoices i JOIN journal_lines l ON l.org_id=i.org_id AND l.journal_id=i.journal_id WHERE i.id=$1 GROUP BY i.total_cents`, id).Scan(&total, &debit); err != nil || total != debit {
		t.Fatalf("invoice total=%d ledger=%d err=%v", total, debit, err)
	}
}
