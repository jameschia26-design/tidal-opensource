package main

import "testing"

// Tax-inclusive invoices store the GST-backed-out lines, and the flag is kept on the row so editing a
// draft reads the typed prices the same way again. An unknown value is refused on create and on update.
func TestAmountsAre(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "amounts@example.com")
	org := orgFromList(t, a, owner, `{"name":"Amounts are","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	line := `{"description":"Taxed","quantity":1,"unit_price_cents":10900,"gst_code":"SR"}`
	body := func(flag string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-03-01","due_date":"2026-03-01"` + flag + `,"lines":[` + line + `]}`
	}
	inclusive := `,"amounts_are":"inclusive"`
	unknown := `,"amounts_are":"both"`
	detail := func(id int64) map[string]any {
		return expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(id)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	}
	if rec := do(t, h, "POST", "/api/v1/invoices", body(unknown), owner); rec.Code != 400 {
		t.Fatalf("unknown amounts_are accepted: %d", rec.Code)
	}
	// Exclusive is the default: the typed 109.00 stays the net and GST is added on top.
	def := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(""), owner), 201)
	if def["total_cents"].(float64) != 11881 {
		t.Fatalf("exclusive default total = %v, want 11881", def["total_cents"])
	}
	if got := detail(int64(def["id"].(float64)))["amounts_are"].(string); got != "exclusive" {
		t.Fatalf("default flag = %q, want exclusive", got)
	}
	// Inclusive: 109.00 holds 100.00 net and 9.00 GST, so the total stays 109.00.
	inc := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body(inclusive), owner), 201)
	if inc["total_cents"].(float64) != 10900 {
		t.Fatalf("inclusive total = %v, want 10900", inc["total_cents"])
	}
	incID := int64(inc["id"].(float64))
	d := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(incID)+"?org_id="+o, "", owner), 200)
	if d["invoice"].(map[string]any)["amounts_are"].(string) != "inclusive" {
		t.Fatalf("inclusive flag = %v", d["invoice"])
	}
	l := d["lines"].([]any)[0].(map[string]any)
	if l["amount_cents"].(float64) != 10000 || l["gst_cents"].(float64) != 900 {
		t.Fatalf("inclusive line = %v, want net 10000 gst 900", l)
	}
	// Updates validate the value too, and a save that omits it keeps the stored one.
	if rec := do(t, h, "PUT", "/api/v1/invoices/"+itoa(incID), body(unknown), owner); rec.Code != 400 {
		t.Fatalf("unknown edit amounts_are accepted: %d", rec.Code)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/invoices/"+itoa(incID), body(""), owner), 200)
	if got := detail(incID)["amounts_are"].(string); got != "inclusive" {
		t.Fatalf("edit without flag = %q, want inclusive", got)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(incID)+"/post?org_id="+o, "", owner), 200)
	if posted := detail(incID); posted["total_cents"].(float64) != 10900 || posted["status"].(string) != "posted" {
		t.Fatalf("posted inclusive = %v", posted)
	}
}
