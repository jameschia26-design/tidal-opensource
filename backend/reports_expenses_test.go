package main

import "testing"

func TestExpenseReports(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "exp-rep-owner@example.com")
	outsider := seedSession(t, a, "exp-rep-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Expenses","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	idOf := func(v map[string]any) string { return itoa(int64(v["id"].(float64))) }
	exp := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Travel","type":"expense"}`, owner), 201))
	bank := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1015","name":"Till","type":"asset"}`, owner), 201))
	cust := idOf(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201))
	spend := func(key, date, amount, extra string) {
		expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"`+date+`","payee":"Cab","description":"Ride","expense_account_id":`+exp+`,"paid_through_account_id":`+bank+`,"amount_cents":`+amount+`,"idempotency_key":"`+key+`"`+extra+`}`, owner), 201)
	}
	spend("exp-a", "2026-10-05", "1500", `,"customer_id":`+cust+`,"billable":true`)
	spend("exp-b", "2026-10-20", "800", "")
	spend("exp-c", "2026-11-02", "4000", "")
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/reports/expense-claim-detail?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", owner), 200)
	rows := detail["rows"].([]any)
	if len(rows) != 2 || detail["total_cents"] != float64(2300) {
		t.Fatal(detail)
	}
	first := rows[0].(map[string]any)
	if first["payee"] != "Cab" || first["account_code"] != "6100" || first["billable"] != true || first["invoiced"] != false {
		t.Fatal(first)
	}
	out := expectStatus(t, do(t, h, "GET", "/api/v1/reports/billable-expenses-outstanding?org_id="+o, "", owner), 200)
	if len(out["rows"].([]any)) != 1 || out["total_cents"] != float64(1500) {
		t.Fatal(out)
	}
	if c := out["rows"].([]any)[0].(map[string]any)["customer"]; c != "Client" {
		t.Fatal(c)
	}
	var billableID string
	for _, raw := range expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+o, "", owner), 200)["expenses"].([]any) {
		e := raw.(map[string]any)
		if e["billable"] == true {
			billableID = idOf(e)
		}
	}
	if billableID == "" {
		t.Fatal("billable expense missing")
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses/"+billableID+"/invoice", `{"org_id":`+o+`}`, owner), 201)
	if len(expectStatus(t, do(t, h, "GET", "/api/v1/reports/billable-expenses-outstanding?org_id="+o, "", owner), 200)["rows"].([]any)) != 0 {
		t.Fatal("invoiced expense still outstanding")
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/expense-claim-detail?org_id="+o+"&from=2026-10-01&to=2026-10-31", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/billable-expenses-outstanding?org_id="+o, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/reports/expense-claim-detail?org_id="+o+"&from=2026-10-31&to=2026-10-01", "", owner), 400)
}
