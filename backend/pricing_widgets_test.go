package main

import "testing"

func TestPricingWidgetSetup(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "widget-owner@example.com")
	other := seedSession(t, a, "widget-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Widgets","fiscal_year_end":"12-31"}`)
	otherOrg := orgFromList(t, a, other, `{"name":"Other Widgets","fiscal_year_end":"12-31"}`)
	product := expectStatus(t, do(t, h, "POST", "/api/v1/billing/products", `{"org_id":`+itoa(org)+`,"name":"Product"}`, owner), 201)
	otherProduct := expectStatus(t, do(t, h, "POST", "/api/v1/billing/products", `{"org_id":`+itoa(otherOrg)+`,"name":"Other"}`, other), 201)
	body := func(p int64) string {
		return `{"org_id":` + itoa(org) + `,"name":"Plans","product_id":` + itoa(p) + `}`
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/billing/pricing-widgets", body(int64(otherProduct["id"].(float64))), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/billing/pricing-widgets", body(int64(product["id"].(float64))), other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/billing/pricing-widgets", body(int64(product["id"].(float64))), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/billing/pricing-widgets", body(int64(product["id"].(float64))), owner), 409)
	list := expectStatus(t, do(t, h, "GET", "/api/v1/billing/pricing-widgets?org_id="+itoa(org), "", owner), 200)["widgets"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["product"] != "Product" {
		t.Fatal(list)
	}
}
