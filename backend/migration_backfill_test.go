package main

import (
	"context"
	"testing"
)

// TestNumberBackfillRunsOverPostedDocuments reproduces the live upgrade failure: a posted invoice and
// a posted bill already exist, and the lifecycle migration must fill their new number columns without
// tripping the immutability guards it installs. The guards must then be active again.
func TestNumberBackfillRunsOverPostedDocuments(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "backfill-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Backfill co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Backfill customer"}`, owner), 201)["id"].(float64)))
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Backfill supplier"}`, owner), 201)["id"].(float64)))
	revenue := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4198","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64)))
	expense := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"5198","name":"Costs","type":"expense"}`, owner), 201)["id"].(float64)))
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+cust+`,"invoice_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Work","revenue_account_id":`+revenue+`,"quantity":1,"unit_price_cents":1000}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(inv["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"BF-1","bill_date":"2026-03-01","due_date":"2026-03-31","lines":[{"description":"Cost","account_id":`+expense+`,"quantity":1,"unit_price_cents":500}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	// Clear the numbers the way an older database would have them, then run the migration's backfill.
	// Each statement is its own transaction: a trigger cannot be re-enabled in the transaction that
	// already fired it.
	if _, err := a.db.Exec(ctx, `ALTER TABLE invoices DISABLE TRIGGER invoices_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(ctx, `ALTER TABLE bills DISABLE TRIGGER bills_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(ctx, `UPDATE invoices SET document_number=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(ctx, `UPDATE bills SET document_number=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(ctx, `ALTER TABLE invoices ENABLE TRIGGER invoices_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(ctx, `ALTER TABLE bills ENABLE TRIGGER bills_guard`); err != nil {
		t.Fatal(err)
	}
	// The migration's own backfill, with the guards active, must now succeed.
	if _, err := a.db.Exec(ctx, `UPDATE invoices SET document_number = number::text WHERE number IS NOT NULL`); err != nil {
		t.Fatalf("invoice backfill over a posted document failed: %v", err)
	}
	if _, err := a.db.Exec(ctx, `UPDATE bills SET document_number = 'BILL-' || lpad(id::text, 5, '0') WHERE document_number IS NULL`); err != nil {
		t.Fatalf("bill backfill over a posted document failed: %v", err)
	}
	var invNumber, billNumber string
	if err := a.db.QueryRow(ctx, `SELECT document_number FROM invoices WHERE id=$1`, int64(inv["id"].(float64))).Scan(&invNumber); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(ctx, `SELECT document_number FROM bills WHERE id=$1`, int64(bill["id"].(float64))).Scan(&billNumber); err != nil {
		t.Fatal(err)
	}
	if invNumber == "" || billNumber == "" {
		t.Fatalf("backfill left numbers empty: invoice %q bill %q", invNumber, billNumber)
	}
	// The guard must be back on: a direct edit of a posted invoice is refused again.
	if _, err := a.db.Exec(ctx, `UPDATE invoices SET notes='changed' WHERE id=$1`, int64(inv["id"].(float64))); err == nil {
		t.Fatal("posted invoice was editable after the backfill")
	}
}
