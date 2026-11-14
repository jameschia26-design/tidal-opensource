package main

import (
	"strings"
	"testing"
)

// Self-assessed codes are priced only from the dated catalogue. Without one they must be rejected,
// never guessed at the statutory 9%.
func TestSelfAssessedCodesNeedCatalogue(t *testing.T) {
	in := billInput{ContactID: 1, SupplierReference: "SR-1", DocumentNumber: "BILL-1", BillDate: "2026-09-30", DueDate: "2026-10-30", Lines: []billLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 10000, AccountID: 1, GSTCode: "IM"}}}
	if _, _, _, _, code := validBillRates(&in, true, 900, nil); code != "unsupported_gst" {
		t.Fatalf("IM without catalogue = %q, want unsupported_gst", code)
	}
}

// IM, LVG and RC each price 9% from the dated catalogue on a bill and keep the supplier total net.
func TestSelfAssessedCodesPriceFromCatalogue(t *testing.T) {
	for _, code := range []string{"IM", "LVG", "RC"} {
		t.Run(code, func(t *testing.T) {
			a, _ := testApp(t)
			h := a.routes()
			owner := seedSession(t, a, "self-"+strings.ToLower(code)+"@example.com")
			org := orgFromList(t, a, owner, `{"name":"Self `+code+`","fiscal_year_end":"12-31"}`)
			if _, err := a.db.Exec(t.Context(), `UPDATE organizations SET gst_registered=true, gst_number='M90300000X' WHERE id=$1`, org); err != nil {
				t.Fatal(err)
			}
			supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Hosting Pte"}`, owner), 201)["id"].(float64))
			exp := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Imported services","type":"expense"}`, owner), 201)
			expID := int64(exp["id"].(float64))
			bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(supplier)+`,"supplier_reference":"`+code+`-1","bill_date":"2026-09-01","due_date":"2026-09-30","lines":[{"description":"x","quantity":1,"unit_price_cents":100000,"account_id":`+itoa(expID)+`,"gst_code":"`+code+`"}]}`, owner), 201)
			if bill["total_cents"].(float64) != 100000 || bill["self_gst_cents"].(float64) != 9000 {
				t.Fatalf("%s totals = %v", code, bill)
			}
		})
	}
}
