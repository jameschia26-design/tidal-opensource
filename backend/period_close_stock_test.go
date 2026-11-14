package main

import "testing"

// TestClosedPeriodLocksStockAndDepreciation covers B4: once a month is closed no dated record in it can
// change, including stock adjustments, stocktakes and depreciation. The database refuses the write even
// when the application check is bypassed, and a close cannot move backwards.
func TestClosedPeriodLocksStockAndDepreciation(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "close-stock@example.com")
	org := orgFromList(t, a, owner, `{"name":"Close stock","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	item := int64(expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+o+`,"name":"Widget","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"sales_account_id":0,"purchase_account_id":0,"track_inventory":true}`, owner), 201)["id"].(float64))
	adjust := func(date string, qty int64) string {
		return `{"org_id":` + o + `,"item_id":` + itoa(item) + `,"adjustment_date":"` + date + `","qty_delta":` + itoa(qty) + `,"unit_cost_cents":500,"reason":"Opening stock"}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", adjust("2026-01-05", 10), owner), 201)

	// The close preview counts what the date would lock before anything changes.
	preview := expectStatus(t, do(t, h, "GET", "/api/v1/period-close?org_id="+o+"&through=2026-01-31", "", owner), 200)
	counts, ok := preview["preview"].(map[string]any)
	if !ok || counts["stock_movements"].(float64) != 1 || counts["journals"].(float64) < 0 {
		t.Fatalf("preview %v", preview)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2026-01-31"}`, owner), 200)

	// Application level: every January stock route and depreciation run is refused.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", adjust("2026-01-20", 1), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/stocktakes", `{"org_id":`+o+`,"taken_on":"2026-01-21","lines":[{"item_id":`+itoa(item)+`,"counted_qty":11}]}`, owner), 422)
	asset := int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Laptop","acquired_on":"2026-01-02","cost_cents":120000,"useful_life_months":12}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-01"}`, owner), 422)

	// Database level: a direct movement insert in the closed month is refused too.
	if _, err := pool.Exec(t.Context(), `INSERT INTO stock_movements(org_id,item_id,movement_date,qty_delta,unit_cost_cents,source_kind,source_id) VALUES($1,$2,'2026-01-15',1,500,'adjustment',999)`, org, item); err == nil {
		t.Fatal("database accepted a stock movement inside a closed month")
	}

	// D9: closed periods do not reopen, so closing earlier is refused and the date stays.
	expectStatus(t, do(t, h, "POST", "/api/v1/period-close", `{"org_id":`+o+`,"closed_through":"2025-12-31"}`, owner), 422)
	got := expectStatus(t, do(t, h, "GET", "/api/v1/period-close?org_id="+o, "", owner), 200)
	if got["closed_through"] != "2026-01-31" {
		t.Fatalf("closed_through moved to %v", got["closed_through"])
	}
	// February stays open, so the same writes succeed there. An asset bought in February has no January
	// month to catch up, so its run posts normally.
	expectStatus(t, do(t, h, "POST", "/api/v1/stock/adjustments", adjust("2026-02-10", 1), owner), 201)
	assetFeb := int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Printer","acquired_on":"2026-02-02","cost_cents":120000,"useful_life_months":12}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-02"}`, owner), 200)
	_ = asset
	_ = assetFeb
}
