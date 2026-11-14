package main

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestQuotesDraftLifecycleAndConversion(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "quote-owner@example.com")
	viewer := seedSession(t, a, "quote-viewer@example.com")
	outsider := seedSession(t, a, "quote-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Quote seller","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='quote-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Quote customer"}`, owner), 201)
	cid := int64(c["id"].(float64))
	input := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cid) + `,"quote_date":"2026-10-29","expiry_date":"2026-11-29","reference":"Q-reference","notes":"Project scope","terms":"Payment within 30 days","lines":[{"description":"Service","quantity":2,"unit_price_cents":12500},{"description":"Included support","quantity":1,"unit_price_cents":0}]}`
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "POST", "/api/v1/quotes", input, token), 403)
	}
	for _, invalid := range []string{strings.Replace(input, "2026-11-29", "2026-10-01", 1), strings.Replace(input, "12500", "-1", 1), strings.Replace(input, "Project scope", `bad\u0000notes`, 1), strings.Replace(input, `"contact_id":`+itoa(cid), `"contact_id":999999`, 1)} {
		expectStatus(t, do(t, h, "POST", "/api/v1/quotes", invalid, owner), 400)
	}
	gstLine := strings.Replace(input, `"description":"Service"`, `"description":"Service","gst_code":"SR"`, 1)
	// An unregistered company cannot quote GST.
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", gstLine, owner), 422)
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"supplier","name":"Not customer"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", strings.Replace(input, `"contact_id":`+itoa(cid), `"contact_id":`+itoa(int64(supplier["id"].(float64))), 1), owner), 400)
	otherOrg := orgFromList(t, a, outsider, `{"name":"Other quotes","fiscal_year_end":"12-31"}`)
	foreign := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(otherOrg)+`,"role":"customer","name":"Foreign customer"}`, outsider), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", strings.Replace(input, `"contact_id":`+itoa(cid), `"contact_id":`+itoa(int64(foreign["id"].(float64))), 1), owner), 400)
	if _, err := pool.Exec(ctx, `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	// GST is optional, not blocked: a registered company may quote with or without GST.
	gstSaved := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", gstLine, owner), 201)
	if gstSaved["total_cents"] != float64(27250) {
		t.Fatal(gstSaved)
	}
	plainSaved := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", input, owner), 201)
	if plainSaved["total_cents"] != float64(25000) {
		t.Fatal(plainSaved)
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET gst_registered=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	q := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", input, owner), 201)
	id := int64(q["id"].(float64))
	path := "/api/v1/quotes/" + itoa(id)
	if q["total_cents"] != float64(25000) {
		t.Fatal(q)
	}
	edited := strings.Replace(input, "12500", "15000", 1)
	expectStatus(t, do(t, h, "PUT", path, edited, owner), 200)
	expectStatus(t, do(t, h, "PUT", path, edited, viewer), 403)
	expectStatus(t, do(t, h, "PUT", "/api/v1/quotes/999999", edited, owner), 404)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/quotes?org_id="+itoa(org), "", viewer), 200)["quotes"].([]any)
	var saved map[string]any
	for _, entry := range listed {
		candidate := entry.(map[string]any)
		if candidate["id"] == float64(id) {
			saved = candidate
		}
	}
	if len(listed) != 3 || saved == nil || saved["total_cents"] != float64(30000) || saved["terms"] != "Payment within 30 days" || len(saved["lines"].([]any)) != 2 {
		t.Fatal(listed)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/quotes?org_id="+itoa(org), "", outsider), 403)
	conv := `{"org_id":` + itoa(org) + `,"invoice_date":"2026-11-01","due_date":"2026-12-01"}`
	expectStatus(t, do(t, h, "POST", path+"/convert", conv, owner), 409)
	stage := func(s string) string { return `{"org_id":` + itoa(org) + `,"stage":"` + s + `"}` }
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("accepted"), owner), 422)
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("sent"), viewer), 403)
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("sent"), owner), 200)
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("sent"), owner), 200)
	expectStatus(t, do(t, h, "PUT", path, edited, owner), 409)
	if _, err := pool.Exec(ctx, `UPDATE quotes SET notes='Changed' WHERE id=$1`, id); err == nil {
		t.Fatal("sent quote mutable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM quotes WHERE id=$1`, id); err == nil {
		t.Fatal("quote deleted")
	}
	if _, err := pool.Exec(ctx, `UPDATE contacts SET name='Renamed customer' WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	listed = expectStatus(t, do(t, h, "GET", "/api/v1/quotes?org_id="+itoa(org), "", owner), 200)["quotes"].([]any)
	for _, entry := range listed {
		if candidate := entry.(map[string]any); candidate["id"] == float64(id) {
			saved = candidate
		}
	}
	if saved["customer_name"] != "Quote customer" || saved["seller_snapshot"].(map[string]any)["name"] != "Quote seller" {
		t.Fatal("snapshots changed", saved)
	}
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("accepted"), owner), 200)
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("rejected"), owner), 422)
	expectStatus(t, do(t, h, "POST", path+"/convert", conv, viewer), 403)
	expectStatus(t, do(t, h, "POST", path+"/convert", strings.Replace(conv, "2026-11-01", "2026-10-01", 1), owner), 400)
	var wg sync.WaitGroup
	codes := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", path+"/convert", conv, owner).Code }()
	}
	wg.Wait()
	close(codes)
	created := 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code != 200 {
			t.Fatal(code)
		}
	}
	if created != 1 {
		t.Fatal("multiple conversions", created)
	}
	invoice := expectStatus(t, do(t, h, "POST", path+"/convert", conv, owner), 200)
	invoiceID := int64(invoice["invoice_id"].(float64))
	inv := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(invoiceID)+"?org_id="+itoa(org), "", owner), 200)
	header := inv["invoice"].(map[string]any)
	if header["status"] != "draft" || header["total_cents"] != float64(30000) || header["reference"] != "Q-reference" || len(inv["lines"].([]any)) != 2 {
		t.Fatal(inv)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id=$1`, org).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&n); err != nil || n != 0 {
		t.Fatal("quote posted money", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='quote.converted'`, org).Scan(&n); err != nil || n != 1 {
		t.Fatal("conversion audit", n, err)
	}
	// Rejected quote cannot convert. Database catches line/header total mismatch even on drafts.
	q = expectStatus(t, do(t, h, "POST", "/api/v1/quotes", input, owner), 201)
	id = int64(q["id"].(float64))
	path = "/api/v1/quotes/" + itoa(id)
	if _, err := pool.Exec(ctx, `UPDATE quotes SET total_cents=1 WHERE id=$1`, id); err == nil {
		t.Fatal("mismatched quote total")
	}
	if _, err := pool.Exec(ctx, `UPDATE quotes SET lines='[{"line_no":1,"description":"x"}]' WHERE id=$1`, id); err == nil {
		t.Fatal("missing quote amounts")
	}
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("sent"), owner), 200)
	expectStatus(t, do(t, h, "POST", path+"/stage", stage("rejected"), owner), 200)
	expectStatus(t, do(t, h, "POST", path+"/convert", conv, owner), 409)

	// GST quote converts with tax carried onto the invoice header and lines.
	if _, err := pool.Exec(ctx, `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	// GST cannot be combined with unallocated discounts, same as invoices.
	withShipping := strings.Replace(gstLine, `"lines":`, `"commercial":{"shipping_cents":100},"lines":`, 1)
	expectStatus(t, do(t, h, "POST", "/api/v1/quotes", withShipping, owner), 422)
	gq := expectStatus(t, do(t, h, "POST", "/api/v1/quotes", gstLine, owner), 201)
	gpath := "/api/v1/quotes/" + itoa(int64(gq["id"].(float64)))
	expectStatus(t, do(t, h, "POST", gpath+"/stage", stage("sent"), owner), 200)
	expectStatus(t, do(t, h, "POST", gpath+"/stage", stage("accepted"), owner), 200)
	gconv := expectStatus(t, do(t, h, "POST", gpath+"/convert", conv, owner), 201)
	ginv := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(int64(gconv["invoice_id"].(float64)))+"?org_id="+itoa(org), "", owner), 200)
	gh := ginv["invoice"].(map[string]any)
	if gh["total_cents"] != float64(27250) {
		t.Fatal(gh)
	}
	var headerGST, lineGST int64
	if err := pool.QueryRow(ctx, `SELECT i.gst_cents,COALESCE(sum(l.gst_cents),0) FROM invoices i JOIN invoice_lines l ON l.invoice_id=i.id WHERE i.id=$1 GROUP BY i.id`, int64(gconv["invoice_id"].(float64))).Scan(&headerGST, &lineGST); err != nil || headerGST != 2250 || lineGST != 2250 {
		t.Fatal("converted GST", headerGST, lineGST, err)
	}
}
