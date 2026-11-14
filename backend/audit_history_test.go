package main

import (
	"strings"
	"testing"
)

// TestAuditHistoryCarriesEntity covers R26: posting, voiding, payment, credit and close events each name
// the record and the acting user, the history filters by those fields and is limited to owners/admins.
func TestAuditHistoryCarriesEntity(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "audit-owner@example.com")
	member := seedSession(t, a, "audit-member@example.com")
	org := orgFromList(t, a, owner, `{"name":"Audit co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'member' FROM users WHERE email='audit-member@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-06-01","due_date":"2026-06-30","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/payments", `{"org_id":`+o+`,"amount_cents":2000,"payment_date":"2026-06-10","idempotency_key":"audit-pay-1"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-06-11","reason":"Return","amount_cents":1000}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2026-06-30"}`, owner), 200)

	// Every event names its record and the user; none of the five is missing an entity.
	events := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o, "", owner), 200)["events"].([]any)
	want := map[string]bool{"invoice.created": false, "invoice.posted": false, "invoice.payment_recorded": false, "credit_note.created": false, "period.closed": false}
	for _, raw := range events {
		e := raw.(map[string]any)
		action, _ := e["action"].(string)
		if _, tracked := want[action]; !tracked {
			continue
		}
		if e["entity_kind"] == nil || e["entity_id"] == nil {
			t.Fatalf("%s has no entity: %v", action, e)
		}
		if e["user_id"] == nil || e["user_email"] != "audit-owner@example.com" {
			t.Fatalf("%s has no acting user: %v", action, e)
		}
		want[action] = true
	}
	for action, seen := range want {
		if !seen {
			t.Fatalf("event %s missing", action)
		}
	}

	// Filters: by record, by user, and by date.
	byRecord := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&entity_kind=invoice&entity_id="+itoa(inv), "", owner), 200)["events"].([]any)
	if len(byRecord) < 2 {
		t.Fatalf("record filter found %d events", len(byRecord))
	}
	for _, raw := range byRecord {
		if raw.(map[string]any)["entity_id"] != float64(inv) {
			t.Fatalf("record filter leaked %v", raw)
		}
	}
	var memberID int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM users WHERE email='audit-member@example.com'`).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	if got := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&user_id="+itoa(memberID), "", owner), 200)["events"].([]any); len(got) != 0 {
		t.Fatalf("user filter returned %d events for a user who did nothing", len(got))
	}
	if got := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&from=2020-01-01&to=2020-01-02", "", owner), 200)["events"].([]any); len(got) != 0 {
		t.Fatalf("date filter returned %d old events", len(got))
	}

	// A void (reversal) carries its record, and the same rows export as CSV.
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	expAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	bill := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"AUD-1","bill_date":"2026-07-02","due_date":"2026-07-31","lines":[{"account_id":`+itoa(expAcct)+`,"description":"Parts","quantity":1,"unit_price_cents":3000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/reverse", `{"org_id":`+o+`,"reversal_date":"2026-07-03"}`, owner), 200)
	voided := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&entity_kind=bill&entity_id="+itoa(bill), "", owner), 200)["events"].([]any)
	var sawVoid bool
	for _, raw := range voided {
		e := raw.(map[string]any)
		if e["action"] == "bill.reversed" {
			sawVoid = true
			if e["entity_kind"] != "bill" || e["entity_id"] != float64(bill) || e["user_id"] == nil {
				t.Fatalf("void event lost its entity: %v", e)
			}
		}
	}
	if !sawVoid {
		t.Fatal("bill.reversed event not found by record filter")
	}
	csv := do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&format=csv", "", owner)
	if csv.Code != 200 || !strings.HasPrefix(csv.Header().Get("Content-Type"), "text/csv") || !strings.Contains(csv.Body.String(), "bill.reversed") {
		t.Fatalf("csv export: %d %q", csv.Code, csv.Header().Get("Content-Type"))
	}

	// Older call sites only put the id in detail; the database fills the entity columns from it.
	expense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-07-05","payee":"Shop","description":"Paper","expense_account_id":`+itoa(expAcct)+`,"amount_cents":500}`, owner), 201)["id"].(float64))
	expEvents := expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&entity_kind=expense&entity_id="+itoa(expense), "", owner), 200)["events"].([]any)
	if len(expEvents) == 0 || expEvents[0].(map[string]any)["entity_id"] != float64(expense) {
		t.Fatalf("expense event missing entity: %v", expEvents)
	}

	// A formula-shaped login email cannot run in the exported spreadsheet (R20 applies to this export too).
	if _, err := a.db.Exec(t.Context(), `INSERT INTO users(email,password_hash) VALUES('=1+1@example.com','x')`); err != nil {
		t.Fatal(err)
	}
	var formulaUser int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM users WHERE email='=1+1@example.com'`).Scan(&formulaUser); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(t.Context(), `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'test.formula', '{}'::jsonb)`, org, formulaUser); err != nil {
		t.Fatal(err)
	}
	csvFormula := do(t, h, "GET", "/api/v1/audit-events?org_id="+o+"&format=csv", "", owner).Body.String()
	if strings.Contains(csvFormula, ",=1+1@example.com,") || !strings.Contains(csvFormula, "'=1+1@example.com") {
		t.Fatalf("exported email is still a formula: %q", csvFormula)
	}

	// Owners and admins only.
	expectStatus(t, do(t, h, "GET", "/api/v1/audit-events?org_id="+o, "", member), 403)
	// The trail stays append-only.
	if _, err := a.db.Exec(t.Context(), `UPDATE audit_events SET action='x' WHERE org_id=$1 AND id=(SELECT min(id) FROM audit_events WHERE org_id=$1)`, org); err == nil {
		t.Fatal("audit event was updated")
	}
	if _, err := a.db.Exec(t.Context(), `DELETE FROM audit_events WHERE org_id=$1`, org); err == nil {
		t.Fatal("audit events were deleted")
	}
}
