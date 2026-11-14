package main

import (
	"strings"
	"testing"
)

func TestItemCatalogRoundTripAndIsolation(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "items-owner@example.com")
	other := seedSession(t, a, "items-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Catalog","fiscal_year_end":"12-31"}`)
	body := `{"org_id":` + itoa(org) + `,"name":"Service fee","item_type":"service","sku":"FEE","unit":"hour","selling_price_cents":1299,"cost_price_cents":500,"sales_description":"Work performed"}`
	created := expectStatus(t, do(t, h, "POST", "/api/v1/items", body, owner), 201)
	if created["selling_price_cents"] != float64(1299) || created["sales_account_id"].(float64) <= 0 {
		t.Fatalf("wrong item: %v", created)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/items", body, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/items", strings.Replace(body, `"sku":"FEE"`, `"sku":"OTHER","track_inventory":true`, 1), owner), 422)
	path := "/api/v1/items/" + itoa(int64(created["id"].(float64)))
	expectStatus(t, do(t, h, "PUT", path, strings.Replace(body, "Service fee", "Updated fee", 1), owner), 200)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/items?org_id="+itoa(org), "", owner), 200)["items"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["name"] != "Updated fee" {
		t.Fatalf("not saved: %v", listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/items?org_id="+itoa(org), "", other), 403)
	expectStatus(t, do(t, h, "PUT", path, body, other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/items", strings.Replace(body, `"sku":"FEE"`, `"sku":"BAD","sales_account_id":999999`, 1), owner), 422)
}
