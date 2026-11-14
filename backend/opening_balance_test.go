package main

import "testing"

// TestOpeningDocumentsReconcile covers R17: opening customer and supplier balances arrive as dated documents,
// so aging equals the control accounts, and a full company export reproduces a balanced ledger.
func TestOpeningDocumentsReconcile(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "opening@example.com")
	org := orgFromList(t, a, owner, `{"name":"Opening","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))

	arDocs := []int64{2000, 2000, 1000}
	for i, amt := range arDocs {
		body := `{"org_id":` + o + `,"kind":"customer","contact_id":` + itoa(cust) + `,"reference":"OPEN-AR-` + itoa(int64(i+1)) + `","doc_date":"2025-12-01","due_date":"2025-12-31","amount_cents":` + itoa(amt) + `}`
		expectStatus(t, do(t, h, "POST", "/api/v1/opening-documents", body, owner), 201)
	}
	for i, amt := range []int64{2000, 1000} {
		body := `{"org_id":` + o + `,"kind":"supplier","contact_id":` + itoa(supp) + `,"reference":"OPEN-AP-` + itoa(int64(i+1)) + `","doc_date":"2025-12-01","due_date":"2025-12-31","amount_cents":` + itoa(amt) + `}`
		expectStatus(t, do(t, h, "POST", "/api/v1/opening-documents", body, owner), 201)
	}
	// Same contact and reference cannot be opened twice.
	dup := `{"org_id":` + o + `,"kind":"customer","contact_id":` + itoa(cust) + `,"reference":"OPEN-AR-1","doc_date":"2025-12-01","due_date":"2025-12-31","amount_cents":2000}`
	expectStatus(t, do(t, h, "POST", "/api/v1/opening-documents", dup, owner), 409)

	sumAging := func(path string) int64 {
		t.Helper()
		got := expectStatus(t, do(t, h, "GET", path+"?org_id="+o+"&as_of=2026-01-31", "", owner), 200)
		var total int64
		for _, raw := range got["rows"].([]any) {
			total += int64(raw.(map[string]any)["balance_cents"].(float64))
		}
		return total
	}
	ledger := func(code string) int64 {
		t.Helper()
		var net int64
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents-l.credit_cents),0) FROM journal_lines l JOIN accounts acc ON acc.org_id=l.org_id AND acc.id=l.account_id WHERE l.org_id=$1 AND acc.code=$2`, org, code).Scan(&net); err != nil {
			t.Fatal(err)
		}
		return net
	}
	if got := sumAging("/api/v1/reports/receivables-aging"); got != 5000 {
		t.Fatalf("AR aging=%d, want 5000", got)
	}
	if got := ledger("1100"); got != 5000 {
		t.Fatalf("AR control=%d, want 5000", got)
	}
	if got := sumAging("/api/v1/reports/payables-aging"); got != 3000 {
		t.Fatalf("AP aging=%d, want 3000", got)
	}
	if got := ledger("2100"); got != -3000 {
		t.Fatalf("AP control=%d, want -3000", got)
	}
	if got := ledger("3900"); got != -2000 {
		t.Fatalf("opening equity=%d, want -2000", got)
	}

	// The export carries the ledger and stays balanced.
	exp := expectStatus(t, do(t, h, "GET", "/api/v1/export/company?org_id="+o, "", owner), 200)["tables"].(map[string]any)
	for _, name := range []string{"journals", "journal_lines", "invoices", "bills", "payments", "bill_payments", "expenses", "audit_events"} {
		if _, ok := exp[name]; !ok {
			t.Fatalf("export missing table %s", name)
		}
	}
	var debit, credit int64
	for _, raw := range exp["journal_lines"].([]any) {
		row := raw.(map[string]any)
		debit += int64(row["debit_cents"].(float64))
		credit += int64(row["credit_cents"].(float64))
	}
	if debit != credit || debit == 0 {
		t.Fatalf("exported ledger debit=%d credit=%d", debit, credit)
	}
}
