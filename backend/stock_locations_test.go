package main

import "testing"

// TestStockLocationsAndTransfers covers the location side of inventory: documents move stock at the
// default location, a transfer moves quantity between locations without changing the total, and the
// per-location report agrees with the item-level report.
func TestStockLocationsAndTransfers(t *testing.T) {
	a, _ := testApp(t)
	mailSink(a)
	h := a.routes()
	owner := seedSession(t, a, "stock-locations@example.com")
	org := orgFromList(t, a, owner, `{"name":"Stock locations","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	item := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","sku":"L1","selling_price_cents":1000,"cost_price_cents":400,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64)))

	locations := expectStatus(t, do(t, h, "GET", "/api/v1/stock/locations?org_id="+o, "", owner), 200)["locations"].([]any)
	if len(locations) != 1 || locations[0].(map[string]any)["is_default"] != true {
		t.Fatalf("initial locations %v", locations)
	}
	mainID := int64(locations[0].(map[string]any)["id"].(float64))
	warehouse := int64(expectStatus(t, do(t, h, "POST", "/api/v1/stock/locations", `{"org_id":`+o+`,"name":"Warehouse"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/locations", `{"org_id":`+o+`,"name":"Warehouse"}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/locations", `{"org_id":`+o+`,"name":"  "}`, owner), 400)

	// A bill puts ten on hand at the default location, because documents carry no location.
	supp := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64)))
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+supp+`,"supplier_reference":"LOC-1","bill_date":"2026-02-01","due_date":"2026-02-01","lines":[{"item_id":`+item+`,"description":"stock","quantity":10,"unit_price_cents":400}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(int64(bill["id"].(float64)))+"/post?org_id="+o, "", owner), 200)

	byLocation := func() map[int64]float64 {
		rep := expectStatus(t, do(t, h, "GET", "/api/v1/reports/stock-by-location?org_id="+o+"&as_of=2026-02-28", "", owner), 200)
		out := map[int64]float64{}
		for _, r := range rep["rows"].([]any) {
			row := r.(map[string]any)
			out[int64(row["location_id"].(float64))] = row["qty"].(float64)
		}
		return out
	}
	got := byLocation()
	if got[mainID] != 10 || got[warehouse] != 0 {
		t.Fatalf("before transfer %v", got)
	}

	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(mainID)+`,"to_location_id":`+itoa(warehouse)+`,"transfer_date":"2026-02-05","qty":4,"note":"To the shelf"}`, owner), 201)
	got = byLocation()
	if got[mainID] != 6 || got[warehouse] != 4 {
		t.Fatalf("after transfer %v", got)
	}
	// The total is unchanged, so the item-level report still says ten.
	total := expectStatus(t, do(t, h, "GET", "/api/v1/reports/inventory-items?org_id="+o+"&as_of=2026-02-28", "", owner), 200)["rows"].([]any)[0].(map[string]any)
	if total["qty"].(float64) != 10 {
		t.Fatalf("total on hand changed to %v", total["qty"])
	}

	// Moving more than the source location holds, or to the same location, is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(warehouse)+`,"to_location_id":`+itoa(mainID)+`,"transfer_date":"2026-02-06","qty":5}`, owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(mainID)+`,"to_location_id":`+itoa(mainID)+`,"transfer_date":"2026-02-06","qty":1}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(mainID)+`,"to_location_id":`+itoa(warehouse)+`,"transfer_date":"2026-02-06","qty":0}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(mainID)+`,"to_location_id":`+itoa(warehouse)+`,"transfer_date":"2026-02-06","qty":1,"note":"`+string(make([]byte, 501))+`"}`, owner), 400)

	// A transfer dated after the report date is not counted there.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/transfers", `{"org_id":`+o+`,"item_id":`+item+`,"from_location_id":`+itoa(mainID)+`,"to_location_id":`+itoa(warehouse)+`,"transfer_date":"2026-03-05","qty":1}`, owner), 201)
	if got := byLocation(); got[mainID] != 6 || got[warehouse] != 4 {
		t.Fatalf("future transfer leaked into the report %v", got)
	}
}
