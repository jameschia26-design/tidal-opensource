package main

import (
	"strings"
	"testing"
)

func TestPurchaseOrdersAndReceives(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "po@example.com")
	other := seedSession(t, a, "po-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Procurement","fiscal_year_end":"12-31"}`)
	otherOrg := orgFromList(t, a, other, `{"name":"Procurement other","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	account := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"name":"Supplies","code":"6400","type":"expense"}`, owner), 201)["id"].(float64))
	vendor := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"name":"Vendor","role":"supplier"}`, owner), 201)["id"].(float64)))
	customer := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"name":"Customer","role":"customer"}`, owner), 201)["id"].(float64)))
	_ = otherOrg
	lines := `[{"description":"Paper","account_id":` + itoa(account) + `,"quantity":10,"unit_price_cents":500},{"description":"Ink","account_id":` + itoa(account) + `,"quantity":2,"unit_price_cents":2500}]`
	po := func(method, path, contact, pdate, ddate, extra string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, method, path, `{"org_id":`+o+`,"contact_id":`+contact+`,"po_date":"`+pdate+`","delivery_date":"`+ddate+`","lines":`+lines+extra+`}`, owner), status)
	}
	po("POST", "/api/v1/purchase-orders", vendor, "2026-10-29", "2026-10-29", "", 400)   // delivery must be after PO date
	po("POST", "/api/v1/purchase-orders", customer, "2026-10-29", "2026-11-05", "", 422) // customer is not a vendor
	body := `{"org_id":` + o + `,"contact_id":` + vendor + `,"po_date":"2026-10-29","delivery_date":"2026-11-05","lines":[{"description":"Bad","account_id":` + itoa(account) + `,"quantity":0,"unit_price_cents":1}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", body, owner), 400)
	created := po("POST", "/api/v1/purchase-orders", vendor, "2026-10-29", "2026-11-05", `,"commercial":{"discount_type":"flat","discount_cents":1000,"shipping_cents":500}`, 201)
	if created["total_cents"] != float64(9500) || created["po_number"] != "PO-00001" {
		t.Fatal(created)
	}
	id := itoa(int64(created["id"].(float64)))
	po("POST", "/api/v1/purchase-orders", vendor, "2026-10-29", "2026-11-05", `,"po_number":"PO-00001"`, 409)
	po("PUT", "/api/v1/purchase-orders/"+id, vendor, "2026-10-29", "2026-11-06", `,"commercial":{"attention":"Receiving desk","telephone":"+65 6000 0000","delivery_instructions":"Use rear gate"}`, 200)
	po("PUT", "/api/v1/purchase-orders/"+id, vendor, "2026-10-29", "2026-11-06", `,"commercial":{"delivery_instructions":"`+strings.Repeat("x", 501)+`"}`, 400)
	pdf := do(t, h, "GET", "/api/v1/documents/purchase-orders/"+id+"/pdf?org_id="+o, "", owner)
	if pdf.Code != 200 || !strings.Contains(pdf.Body.String(), "Use rear gate") || !strings.Contains(pdf.Body.String(), "Receiving desk") {
		t.Fatal("delivery contact missing from PDF", pdf.Code)
	}

	receive := func(extra string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "POST", "/api/v1/purchase-receives", `{"org_id":`+o+`,"purchase_order_id":`+id+`,"received_date":"2026-11-06"`+extra+`}`, owner), status)
	}
	receive(`,"items":[{"line_no":1,"quantity":1}]`, 422) // still draft
	stage := func(path, to string, who string, status int) {
		t.Helper()
		expectStatus(t, do(t, h, "POST", path, `{"org_id":`+o+`,"stage":"`+to+`"}`, who), status)
	}
	stage("/api/v1/purchase-orders/"+id+"/stage", "issued", other, 403)
	stage("/api/v1/purchase-orders/"+id+"/stage", "issued", owner, 200)
	po("PUT", "/api/v1/purchase-orders/"+id, vendor, "2026-10-29", "2026-11-06", "", 409) // issued POs are frozen

	receive(`,"items":[{"line_no":1,"quantity":11}]`, 422) // more than ordered
	receive(`,"items":[{"line_no":3,"quantity":1}]`, 422)  // unknown line
	receive(`,"tracking_url":"javascript:alert(1)","items":[{"line_no":1,"quantity":1}]`, 400)
	draft := receive(`,"tracking_number":"TRK1","tracking_url":"https://track.example/1","items":[{"line_no":1,"quantity":6}]`, 201)
	receive(`,"items":[{"line_no":1,"quantity":5}]`, 422) // 6 in transit + 5 > 10
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/purchase-orders?org_id="+o, "", owner), 200)["purchase_orders"].([]any)[0].(map[string]any)
	if listed["in_transit"].(map[string]any)["1"] != float64(6) {
		t.Fatal(listed)
	}
	stage("/api/v1/purchase-orders/"+id+"/stage", "cancelled", owner, 422) // has receives
	rid := itoa(int64(draft["id"].(float64)))
	stage("/api/v1/purchase-receives/"+rid+"/stage", "received", owner, 200)
	receive(`,"status":"received","items":[{"line_no":1,"quantity":4},{"line_no":2,"quantity":1}]`, 201)
	poID := int64(created["id"].(float64))
	state := func() string {
		var s string
		if err := pool.QueryRow(t.Context(), `SELECT status FROM purchase_orders WHERE id=$1`, poID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if state() != "issued" {
		t.Fatal("po closed too early")
	}
	receive(`,"status":"received","items":[{"line_no":2,"quantity":1}]`, 201)
	if state() != "received" {
		t.Fatal("po should be fully received")
	}
	receive(`,"items":[{"line_no":2,"quantity":1}]`, 422) // no longer issued

	// nothing posted to the ledger
	var journals int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil || journals != 0 {
		t.Fatal(journals, err)
	}
	// attachments work for both kinds
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/purchase-orders/"+id+"/attachments?org_id="+o, "", owner), 200)
}
