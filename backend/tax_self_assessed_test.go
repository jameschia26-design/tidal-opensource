package main

import "testing"

// TestSelfAssessedPurchaseTax proves an imported-service bill adds matching input and output GST
// legs without increasing what the supplier is owed, and that a custom catalogue rate can be added.
func TestSelfAssessedPurchaseTax(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "self-tax@example.com")
	org := orgFromList(t, a, owner, `{"name":"Self tax","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	if _, err := a.db.Exec(t.Context(), `UPDATE organizations SET gst_registered=true, gst_number='M90300000X' WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Hosting Pte"}`, owner), 201)["id"].(float64))
	exp := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Imported services","type":"expense"}`, owner), 201)
	expID := itoa(int64(exp["id"].(float64)))

	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supplier)+`,"supplier_reference":"IM-1","bill_date":"2026-09-01","due_date":"2026-09-30","lines":[{"description":"Hosting","quantity":1,"unit_price_cents":100000,"account_id":`+expID+`,"gst_code":"IM"}]}`, owner), 201)
	if bill["total_cents"].(float64) != 100000 || bill["self_gst_cents"].(float64) != 9000 || bill["gst_cents"].(float64) != 0 {
		t.Fatalf("self-assessed bill totals = %v", bill)
	}
	id := itoa(int64(bill["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+id+"/post?org_id="+o, "", owner), 200)
	var input, output int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(SUM(CASE WHEN jl.tax_direction='input' THEN jl.debit_cents ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN jl.tax_direction='output' THEN jl.credit_cents ELSE 0 END),0)
		FROM journal_lines jl JOIN bills b ON b.org_id=jl.org_id AND b.journal_id=jl.journal_id WHERE b.org_id=$1 AND b.id=$2`, org, id).Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	if input != 9000 || output != 9000 {
		t.Fatalf("self-assessed journal input=%d output=%d, want 9000 each", input, output)
	}
	var ap int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(jl.credit_cents-jl.debit_cents),0) FROM journal_lines jl JOIN accounts ac ON ac.org_id=jl.org_id AND ac.id=jl.account_id JOIN bills b ON b.org_id=jl.org_id AND b.journal_id=jl.journal_id WHERE b.org_id=$1 AND b.id=$2 AND ac.code=$3`, org, id, acctAP).Scan(&ap); err != nil {
		t.Fatal(err)
	}
	if ap != 100000 {
		t.Fatalf("payable %d, want supplier net 100000", ap)
	}

	rate := expectStatus(t, do(t, h, "POST", "/api/v1/tax/rates", `{"org_id":`+o+`,"code":"CUSTOM9","name":"Custom nine","rate_bp":900,"kind":"standard","applies_to":"sale","effective_from":"2026-01-01"}`, owner), 201)
	if rate["code"] != "CUSTOM9" {
		t.Fatalf("custom rate = %v", rate)
	}
}
