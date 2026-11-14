package main

import "testing"

func TestConvertBillableExpenseToDraftInvoice(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "expense-invoice-owner@example.com")
	other := seedSession(t, a, "expense-invoice-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Reimburse Co","fiscal_year_end":"12-31"}`)
	otherOrg := orgFromList(t, a, other, `{"name":"Other Reimburse Co","fiscal_year_end":"12-31"}`)
	customer := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Buyer"}`, owner), 201)
	account := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"code":"6100","name":"Travel","type":"expense"}`, owner), 201)
	create := func(billable bool) int64 {
		body := `{"org_id":` + itoa(org) + `,"expense_date":"2026-10-15","payee":"Taxi","description":"Airport ride","expense_account_id":` + itoa(int64(account["id"].(float64))) + `,"amount_cents":2500,"customer_id":` + itoa(int64(customer["id"].(float64))) + `,"billable":` + boolString(billable) + `}`
		made := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body, owner), 201)
		return int64(made["id"].(float64))
	}
	// A non-billable expense is rejected.
	nonbillable := create(false)
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses/"+itoa(nonbillable)+"/invoice", `{"org_id":`+itoa(org)+`}`, owner), 422)
	billable := create(true)
	path := "/api/v1/expenses/" + itoa(billable) + "/invoice"
	// Another company cannot see or convert this expense; the lookup is scoped to the caller's company, so it reports not_found.
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(otherOrg)+`}`, other), 404)
	made := expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`}`, owner), 201)
	invoiceID := int64(made["invoice_id"].(float64))
	expectStatus(t, do(t, h, "POST", path, `{"org_id":`+itoa(org)+`}`, owner), 409)
	var status, description string
	var total int64
	if err := pool.QueryRow(t.Context(), `SELECT i.status,i.total_cents,l.description FROM invoices i JOIN invoice_lines l ON l.org_id=i.org_id AND l.invoice_id=i.id WHERE i.org_id=$1 AND i.id=$2`, org, invoiceID).Scan(&status, &total, &description); err != nil || status != "draft" || total != 2500 || description != "Airport ride" {
		t.Fatal(status, total, description, err)
	}
	var linked int64
	if err := pool.QueryRow(t.Context(), `SELECT invoice_id FROM expense_invoice_links WHERE org_id=$1 AND expense_id=$2`, org, billable).Scan(&linked); err != nil || linked != invoiceID {
		t.Fatal(linked, err)
	}
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// GST is chosen at conversion: optional, registered companies only, and the expense amount is the price before GST.
func TestConvertBillableExpenseGST(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "expense-invoice-gst@example.com")
	plainOwner := seedSession(t, a, "expense-invoice-gst-plain@example.com")
	org := orgFromList(t, a, owner, `{"name":"GST Reimburse Co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	plain := orgFromList(t, a, plainOwner, `{"name":"Plain Reimburse Co","fiscal_year_end":"12-31"}`)
	make1 := func(o int64, tok, accountCode string) int64 {
		c := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(o)+`,"role":"customer","name":"Buyer"}`, tok), 201)
		ac := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(o)+`,"code":"`+accountCode+`","name":"Travel","type":"expense"}`, tok), 201)
		body := `{"org_id":` + itoa(o) + `,"expense_date":"2026-10-15","payee":"Taxi","description":"Airport ride","expense_account_id":` + itoa(int64(ac["id"].(float64))) + `,"amount_cents":10000,"customer_id":` + itoa(int64(c["id"].(float64))) + `,"billable":true}`
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/expenses", body, tok), 201)["id"].(float64))
	}
	e1 := make1(org, owner, "6100")
	p1 := make1(plain, plainOwner, "6100")
	post := func(o, e int64, tok, extra string, want int) map[string]any {
		return expectStatus(t, do(t, h, "POST", "/api/v1/expenses/"+itoa(e)+"/invoice", `{"org_id":`+itoa(o)+extra+`}`, tok), want)
	}
	// Refused: unregistered company, unknown code. Nothing is linked after a refusal.
	if m := post(plain, p1, plainOwner, `,"gst_code":"SR"`, 422); m["error"] != "unsupported_gst" {
		t.Fatal(m)
	}
	if m := post(org, e1, owner, `,"gst_code":"ZZ"`, 422); m["error"] != "unsupported_gst" {
		t.Fatal(m)
	}
	// Omitted code keeps old no-GST behaviour, even for a registered company.
	e2 := make1(org, owner, "6101")
	n := post(org, e2, owner, ``, 201)
	var total, gst int64
	if err := pool.QueryRow(t.Context(), `SELECT total_cents,gst_cents FROM invoices WHERE org_id=$1 AND id=$2`, org, int64(n["invoice_id"].(float64))).Scan(&total, &gst); err != nil || total != 10000 || gst != 0 {
		t.Fatal(total, gst, err)
	}
	// SR on a registered company: 9% on the pre-GST amount, saved on the line and header.
	m := post(org, e1, owner, `,"gst_code":"SR"`, 201)
	id := int64(m["invoice_id"].(float64))
	var code string
	var rate, lineGST int64
	if err := pool.QueryRow(t.Context(), `SELECT i.total_cents,i.gst_cents,l.gst_code,l.gst_rate_bp,l.gst_cents FROM invoices i JOIN invoice_lines l ON l.org_id=i.org_id AND l.invoice_id=i.id WHERE i.org_id=$1 AND i.id=$2`, org, id).Scan(&total, &gst, &code, &rate, &lineGST); err != nil || total != 10900 || gst != 900 || code != "SR" || rate != 900 || lineGST != 900 {
		t.Fatal(total, gst, code, rate, lineGST, err)
	}
	post(org, e1, owner, `,"gst_code":"SR"`, 409)
	// The GST draft posts through the normal invoice flow.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+itoa(org), ``, owner), 200)
}
