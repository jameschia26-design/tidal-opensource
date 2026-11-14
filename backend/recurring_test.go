package main

import (
	"context"
	"testing"
)

func TestRecurringInvoicesGenerateDraftsOnce(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "recur-owner@example.com")
	viewer := seedSession(t, a, "recur-viewer@example.com")
	outsider := seedSession(t, a, "recur-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Recurring co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='recur-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Retainer client"}`, owner), 201)
	cid := int64(c["id"].(float64))
	body := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cid) + `,"name":"Monthly retainer","frequency":"monthly","start_date":"2026-11-01","term_days":30,"reference":"RET","lines":[{"description":"Retainer","quantity":1,"unit_price_cents":50000}]}`
	for _, token := range []string{viewer, outsider} {
		expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, token), 403)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(cid)+`,"name":"Bad","frequency":"weekly","start_date":"2026-11-01","term_days":30,"lines":[{"description":"x","quantity":1,"unit_price_cents":1}]}`, owner), 400)
	tmpl := expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 201)
	if tmpl["next_run"] != "2026-11-01" || tmpl["total_cents"] != float64(50000) {
		t.Fatal(tmpl)
	}
	// Before the first run date nothing is generated.
	none := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-10-31"}`, owner), 200)
	if ids := none["created_invoice_ids"].([]any); len(ids) != 0 {
		t.Fatal("ran early", ids)
	}
	first := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-11-01"}`, owner), 200)
	if ids := first["created_invoice_ids"].([]any); len(ids) != 1 {
		t.Fatal("first run", first)
	}
	// Re-running for the same date must not create a second draft.
	again := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-11-01"}`, owner), 200)
	if ids := again["created_invoice_ids"].([]any); len(ids) != 0 {
		t.Fatal("duplicate draft on repeat run", again)
	}
	var status, reference string
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*), max(status), max(reference) FROM invoices WHERE org_id=$1 AND recurring_template_id IS NOT NULL`, org).Scan(&n, &status, &reference); err != nil {
		t.Fatal(err)
	}
	if n != 1 || status != "draft" || reference != "RET" {
		t.Fatal("expected exactly one draft with its reference", n, status, reference)
	}
	// Next run advanced to 2026-12-01; a run on 2026-12-01 creates the second draft only.
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/recurring?org_id="+itoa(org), "", viewer), 200)["templates"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["next_run"] != "2026-12-01" {
		t.Fatal("schedule not advanced", listed)
	}
	second := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-12-01"}`, owner), 200)
	if ids := second["created_invoice_ids"].([]any); len(ids) != 1 {
		t.Fatal("second run", second)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-12-01"}`, viewer), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/recurring?org_id="+itoa(org), "", outsider), 403)
}

func TestRecurringInvoiceMonthEnd(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recurring-month-end@example.com")
	org := orgFromList(t, a, owner, `{"name":"Monthly dates","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := itoa(int64(c["id"].(float64)))
	body := `{"org_id":` + o + `,"contact_id":` + cid + `,"name":"Month end","frequency":"monthly","start_date":"2026-01-31","term_days":30,"lines":[{"description":"Service","quantity":1,"unit_price_cents":100}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 201)
	for _, check := range [][2]string{{"2026-01-31", "2026-02-28"}, {"2026-02-28", "2026-03-31"}, {"2026-05-31", "2026-06-30"}} {
		expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"`+check[0]+`"}`, owner), 200)
		templates := expectStatus(t, do(t, h, "GET", "/api/v1/recurring?org_id="+o, "", owner), 200)["templates"].([]any)
		if got := templates[0].(map[string]any)["next_run"]; got != check[1] {
			t.Fatalf("after %s got %v, want %s", check[0], got, check[1])
		}
	}
}

func TestRecurringInvoiceEndDate(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := context.Background()
	owner := seedSession(t, a, "recur-end@example.com")
	org := orgFromList(t, a, owner, `{"name":"Recurring end co","fiscal_year_end":"12-31"}`)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"End client"}`, owner), 201)
	cid := int64(c["id"].(float64))
	base := `{"org_id":` + itoa(org) + `,"contact_id":` + itoa(cid) + `,"name":"Ending retainer","frequency":"monthly","start_date":"2026-11-01","term_days":30,"reference":"END","lines":[{"description":"Retainer","quantity":1,"unit_price_cents":10000}]`
	// End date must be after the first run date.
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", base+`,"end_date":"2026-11-01"}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", base+`,"end_date":"2026-12-01"}`, owner), 201)
	// Runs stop once next_run passes the end date: November and December only.
	nov := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-11-01"}`, owner), 200)
	if ids := nov["created_invoice_ids"].([]any); len(ids) != 1 {
		t.Fatal("november run", nov)
	}
	dec := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2026-12-01"}`, owner), 200)
	if ids := dec["created_invoice_ids"].([]any); len(ids) != 1 {
		t.Fatal("december run", dec)
	}
	done := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+itoa(org)+`,"as_of":"2027-06-01"}`, owner), 200)
	if ids := done["created_invoice_ids"].([]any); len(ids) != 0 {
		t.Fatal("generated past the end date", ids)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE org_id=$1 AND recurring_template_id IS NOT NULL`, org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatal("expected two drafts", n)
	}
}

func TestRecurringInvoiceGST(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recurring-gst@example.com")
	org := orgFromList(t, a, owner, `{"name":"Recurring GST","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := itoa(int64(c["id"].(float64)))
	body := `{"org_id":` + o + `,"contact_id":` + cid + `,"name":"GST","frequency":"yearly","start_date":"2023-01-01","term_days":30,"lines":[{"description":"Service","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 422)
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	saved := expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 201)
	id := int64(saved["id"].(float64))
	for _, tc := range []struct {
		date             string
		total, gst, rate int64
	}{{"2023-01-01", 10800, 800, 800}, {"2024-01-01", 10900, 900, 900}} {
		r := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"`+tc.date+`"}`, owner), 200)
		if len(r["created_invoice_ids"].([]any)) != 1 {
			t.Fatal(r)
		}
		var total, gst, rate, lineGST int64
		var code string
		if err := pool.QueryRow(t.Context(), `SELECT i.total_cents,i.gst_cents,l.gst_code,l.gst_rate_bp,l.gst_cents FROM invoices i JOIN invoice_lines l ON l.invoice_id=i.id WHERE i.org_id=$1 AND i.recurring_template_id=$2 AND i.invoice_date=$3`, org, id, tc.date).Scan(&total, &gst, &code, &rate, &lineGST); err != nil {
			t.Fatal(err)
		}
		if total != tc.total || gst != tc.gst || lineGST != gst || rate != tc.rate || code != "SR" {
			t.Fatal(total, gst, code, rate, lineGST)
		}
	}
}

func TestRecurringMultiLineTemplate(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recur-multiline@example.com")
	org := orgFromList(t, a, owner, `{"name":"Multiline co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := itoa(int64(c["id"].(float64)))
	body := `{"org_id":` + o + `,"contact_id":` + cid + `,"name":"Two lines","frequency":"monthly","start_date":"2026-11-01","term_days":30,"lines":[{"description":"Retainer","quantity":1,"unit_price_cents":50000},{"description":"Hosting","quantity":2,"unit_price_cents":10000}]}`
	saved := expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 201)
	if saved["total_cents"] != float64(70000) {
		t.Fatal(saved)
	}
	id := int64(saved["id"].(float64))
	r := expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"2026-11-01"}`, owner), 200)
	if ids := r["created_invoice_ids"].([]any); len(ids) != 1 {
		t.Fatal("run", r)
	}
	var n int
	var descs string
	if err := pool.QueryRow(t.Context(), `SELECT count(*), string_agg(l.description,',' ORDER BY l.line_no) FROM invoice_lines l JOIN invoices i ON i.id=l.invoice_id WHERE i.org_id=$1 AND i.recurring_template_id=$2`, org, id).Scan(&n, &descs); err != nil {
		t.Fatal(err)
	}
	if n != 2 || descs != "Retainer,Hosting" {
		t.Fatalf("got %d lines %q, want 2 Retainer,Hosting", n, descs)
	}
}

func TestRecurringDueDay(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recur-dueday@example.com")
	org := orgFromList(t, a, owner, `{"name":"Due day co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Customer"}`, owner), 201)
	cid := itoa(int64(c["id"].(float64)))
	mk := func(name, start string, dueDay string) int64 {
		body := `{"org_id":` + o + `,"contact_id":` + cid + `,"name":"` + name + `","frequency":"monthly","start_date":"` + start + `","term_days":30,"due_day":` + dueDay + `,"lines":[{"description":"Service","quantity":1,"unit_price_cents":1000}]}`
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/recurring", body, owner), 201)["id"].(float64))
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring", `{"org_id":`+o+`,"contact_id":`+cid+`,"name":"Bad","frequency":"monthly","start_date":"2026-11-01","term_days":30,"due_day":32,"lines":[{"description":"x","quantity":1,"unit_price_cents":1}]}`, owner), 400)
	id15 := mk("Day 15", "2026-11-01", "15")
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"2026-11-01"}`, owner), 200)
	var due string
	if err := pool.QueryRow(t.Context(), `SELECT due_date::text FROM invoices WHERE org_id=$1 AND recurring_template_id=$2`, org, id15).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != "2026-12-15" {
		t.Fatalf("due = %s, want 2026-12-15", due)
	}
	// 31 clamps to the last day of short February 2027 (non-leap year).
	id31 := mk("Day 31", "2027-01-15", "31")
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring/run", `{"org_id":`+o+`,"as_of":"2027-01-15"}`, owner), 200)
	if err := pool.QueryRow(t.Context(), `SELECT due_date::text FROM invoices WHERE org_id=$1 AND recurring_template_id=$2`, org, id31).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != "2027-02-28" {
		t.Fatalf("due = %s, want 2027-02-28", due)
	}
}
