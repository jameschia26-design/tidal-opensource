package main

import (
	"net/http"
	"strings"
	"testing"
)

// Purchase orders: GST is optional per line, only for GST-registered companies, and is added to the stored total.
func TestPurchaseOrderGST(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-po@example.com")
	plainOwner := seedSession(t, a, "gst-po-plain@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST PO Test","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	plain := orgFromList(t, a, plainOwner, `{"name":"Plain PO Test","fiscal_year_end":"12-31"}`)
	setup := func(o int64, tok string) (vendor, acct int64) {
		vendor = int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(o)+`,"name":"Vendor","role":"supplier"}`, tok), 201)["id"].(float64))
		acct = int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(o)+`,"code":"6400","name":"Supplies","type":"expense"}`, tok), 201)["id"].(float64))
		return
	}
	vendor, acct := setup(org, owner)
	pvendor, pacct := setup(plain, plainOwner)
	body := func(o, v, a int64, code, extra string) string {
		return `{"org_id":` + itoa(o) + `,"contact_id":` + itoa(v) + `,"po_date":"2026-10-30","delivery_date":"2026-11-06","lines":[{"description":"Paper","account_id":` + itoa(a) + `,"quantity":1,"unit_price_cents":10000,"gst_code":"` + code + `"}]` + extra + `}`
	}
	refused := func(b, tok string) {
		t.Helper()
		if m := expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", b, tok), http.StatusUnprocessableEntity); m["error"] != "unsupported_gst" {
			t.Fatal(m)
		}
	}
	refused(body(plain, pvendor, pacct, "SR", ""), plainOwner)
	refused(body(org, vendor, acct, "ZZ", ""), owner)
	refused(body(org, vendor, acct, "SR", `,"commercial":{"shipping_cents":100}`), owner)

	// Registered company without tax on the line keeps the old behaviour.
	if m := expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", body(org, vendor, acct, "NONE", ""), owner), 201); m["total_cents"] != float64(10000) {
		t.Fatal(m)
	}
	made := expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", body(org, vendor, acct, "SR", ""), owner), 201)
	if made["total_cents"] != float64(10900) {
		t.Fatal(made)
	}
	id := itoa(int64(made["id"].(float64)))
	// Edit a draft: dropping the tax drops the total.
	if m := expectStatus(t, do(t, h, "PUT", "/api/v1/purchase-orders/"+id, body(org, vendor, acct, "NONE", ""), owner), 200); m["total_cents"] != float64(10000) {
		t.Fatal(m)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/purchase-orders/"+id, body(org, vendor, acct, "SR", ""), owner), 200)

	listed := expectStatus(t, do(t, h, "GET", "/api/v1/purchase-orders?org_id="+itoa(org), "", owner), 200)["purchase_orders"].([]any)[0].(map[string]any)
	line := listed["lines"].([]any)[0].(map[string]any)
	if listed["total_cents"] != float64(10900) || line["gst_code"] != "SR" || line["gst_cents"] != float64(900) || line["gst_rate_bp"] != float64(900) {
		t.Fatal(listed)
	}
	pdf := do(t, h, "GET", "/api/v1/documents/purchase-orders/"+id+"/pdf?org_id="+itoa(org), "", owner)
	if pdf.Code != 200 || !strings.Contains(pdf.Body.String(), "GST SGD 9.00") {
		t.Fatal("GST missing from PDF", pdf.Code)
	}
	// A PO posts nothing: no journal, and the stored total is net plus GST.
	var journals, ok int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM journals WHERE org_id=$1), (SELECT count(*) FROM purchase_orders WHERE org_id=$1 AND total_cents=quote_line_subtotal(lines)+quote_line_gst(lines))`, org).Scan(&journals, &ok); err != nil || journals != 0 || ok != 2 {
		t.Fatal(journals, ok, err)
	}
}
