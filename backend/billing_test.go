package main

import "testing"

func TestBillingCatalogAndSubscriptions(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bill-owner@example.com")
	other := seedSession(t, a, "bill-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Billing","fiscal_year_end":"12-31"}`)
	o := `"org_id":` + itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	post := func(path, body string, who string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "POST", "/api/v1/billing/"+path, `{`+o+`,`+body+`}`, who), status)
	}
	cust := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{`+o+`,"role":"customer","name":"Subscriber"}`, owner), 201))
	sup := id(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201))
	exp := id(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{`+o+`,"code":"6900","name":"Rent","type":"expense"}`, owner), 201))

	post("products", `"name":"Cloud"`, other, 403)
	prod := id(post("products", `"name":"Cloud"`, owner, 201))
	post("products", `"name":"cloud"`, owner, 409)
	post("plans", `"product_id":999,"name":"Basic","code":"basic","interval_count":1,"interval_unit":"month","price_cents":1000,"item_type":"service"`, owner, 422)
	post("plans", `"product_id":`+itoa(prod)+`,"name":"Basic","code":"bad code","interval_count":1,"interval_unit":"month","price_cents":1000,"item_type":"service"`, owner, 400)
	post("plans", `"product_id":`+itoa(prod)+`,"name":"Basic","code":"basic","interval_count":1,"interval_unit":"month","price_cents":1000,"item_type":"service","plan_account_id":`+itoa(exp), owner, 422)
	post("plans", `"product_id":`+itoa(prod)+`,"name":"Basic","code":"basic","interval_count":1,"interval_unit":"month","price_cents":1000,"item_type":"service","tax":"GST9"`, owner, 422)
	plan := id(post("plans", `"product_id":`+itoa(prod)+`,"name":"Basic","code":"basic","interval_count":1,"interval_unit":"month","billing_cycles":3,"price_cents":1000,"setup_fee_cents":500,"trial_days":0,"item_type":"service","unit_name":"seat"`, owner, 201))
	post("plans", `"product_id":`+itoa(prod)+`,"name":"Basic 2","code":"BASIC","interval_count":1,"interval_unit":"month","price_cents":1,"item_type":"service"`, owner, 409)
	post("addons", `"product_id":`+itoa(prod)+`,"name":"Support","code":"sup","addon_type":"recurring","interval_count":1,"interval_unit":"month","price_cents":300,"item_type":"service","plan_ids":[777]`, owner, 422)
	rec := id(post("addons", `"product_id":`+itoa(prod)+`,"name":"Support","code":"sup","addon_type":"recurring","interval_count":1,"interval_unit":"month","price_cents":300,"item_type":"service","plan_ids":[`+itoa(plan)+`]`, owner, 201))
	one := id(post("addons", `"product_id":`+itoa(prod)+`,"name":"Onboarding","code":"onb","addon_type":"one_time","price_cents":2000,"item_type":"service"`, owner, 201))
	post("coupons", `"product_id":`+itoa(prod)+`,"name":"Bad","code":"bad","discount_type":"percent","discount_basis_points":10000,"redemption_type":"forever","applicability":"both"`, owner, 400)
	post("coupons", `"product_id":`+itoa(prod)+`,"name":"Bad","code":"bad","discount_type":"flat","discount_cents":100,"redemption_type":"limited","applicability":"both"`, owner, 400)
	post("coupons", `"product_id":`+itoa(prod)+`,"name":"Bad","code":"bad","discount_type":"flat","discount_cents":100,"redemption_type":"forever","applicability":"both","addon_ids":[`+itoa(one)+`]`, owner, 422)
	coupon := id(post("coupons", `"product_id":`+itoa(prod)+`,"name":"Launch","code":"LAUNCH","discount_type":"flat","discount_cents":400,"redemption_type":"one_time","applicability":"plan","max_redemptions":1`, owner, 201))
	_ = coupon

	sub := func(body string, status int) map[string]any {
		t.Helper()
		return post("subscriptions", body, owner, status)
	}
	sub(`"contact_id":`+itoa(sup)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-01-31"`, 422)
	sub(`"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-01-31","addons":[{"addon_id":`+itoa(rec)+`,"quantity":0}]`, 422)
	sub(`"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-01-31","coupon_code":"NOPE"`, 422)
	// cycle 1: 2x1000 + 500 setup + 2x300 addon + 2000 onboarding = 5100, coupon takes 400 off plan lines = 4700
	made := sub(`"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":2,"start_date":"2026-01-31","coupon_code":"launch","addons":[{"addon_id":`+itoa(rec)+`,"quantity":2},{"addon_id":`+itoa(one)+`,"quantity":1}]`, 201)
	// coupon capped at 1 redemption
	sub(`"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-01-31","coupon_code":"LAUNCH"`, 422)

	runs := func(asOf string) map[string]any {
		return post("subscriptions/run", `"as_of":"`+asOf+`"`, owner, 200)
	}
	r1 := runs("2026-01-31")
	ids := r1["created_invoice_ids"].([]any)
	if len(ids) != 1 {
		t.Fatal(r1)
	}
	var total int64
	if err := pool.QueryRow(t.Context(), `SELECT total_cents FROM invoices WHERE id=$1`, int64(ids[0].(float64))).Scan(&total); err != nil || total != 4700 {
		t.Fatal(total, err)
	}
	if again := runs("2026-01-31")["created_invoice_ids"].([]any); len(again) != 0 {
		t.Fatal("same day created another")
	}
	// next cycle is 28 Feb (31st clamps), cycle 2 has no setup, no one-time, no coupon: 2x1000 + 2x300 = 2600
	if r2 := runs("2026-03-15")["created_invoice_ids"].([]any); len(r2) != 1 {
		t.Fatal(r2)
	}
	if r3 := runs("2026-04-01")["created_invoice_ids"].([]any); len(r3) != 1 {
		t.Fatal(r3)
	}
	var d1, d2 string
	var t2 int64
	if err := pool.QueryRow(t.Context(), `SELECT invoice_date::text,total_cents FROM invoices WHERE subscription_id=$1 AND subscription_cycle=2`, id(made)).Scan(&d1, &t2); err != nil || d1 != "2026-02-28" || t2 != 2600 {
		t.Fatal(d1, t2, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT invoice_date::text FROM invoices WHERE subscription_id=$1 AND subscription_cycle=3`, id(made)).Scan(&d2); err != nil || d2 != "2026-03-31" {
		t.Fatal(d2, err)
	}
	// billing_cycles=3 reached: completed, no more runs
	list := expectStatus(t, do(t, h, "GET", "/api/v1/billing/subscriptions?org_id="+itoa(org), "", owner), 200)["rows"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	first := list[0].(map[string]any)
	if first["status"] != "completed" || first["customer_name"] != "Subscriber" {
		t.Fatal(first)
	}
	if n := len(runs("2027-01-01")["created_invoice_ids"].([]any)); n != 0 {
		t.Fatal(n)
	}
	post("subscriptions/"+itoa(id(made))+"/stage", `"stage":"cancel"`, owner, 409)

	// items mode, cancel
	it := sub(`"contact_id":`+itoa(cust)+`,"mode":"items","interval_count":1,"interval_unit":"week","start_date":"2026-05-01","items":[{"description":"Cleaning","quantity":1,"unit_price_cents":700}]`, 201)
	post("subscriptions/"+itoa(id(it))+"/stage", `"stage":"cancel"`, owner, 200)
	if n := len(runs("2026-06-01")["created_invoice_ids"].([]any)); n != 0 {
		t.Fatal("cancelled subscription billed", n)
	}
	// deactivated plan cannot be subscribed to
	post("plans/"+itoa(plan)+"/stage", `"stage":"deactivate"`, owner, 200)
	sub(`"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-07-01"`, 422)
	post("plans/"+itoa(plan)+"/stage", `"stage":"deactivate"`, other, 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/billing/plans?org_id="+itoa(org), "", other), 403)
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/billing-plans/"+itoa(plan)+"/attachments?org_id="+itoa(org), "", owner), 200)
	expectStatus(t, do(t, h, "GET", "/api/v1/entities/credit-notes/1/attachments?org_id="+itoa(org), "", owner), 404)
}

func TestSubscriptionInvoicePostsToPlanAccounts(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bill-accounts@example.com")
	org := orgFromList(t, a, owner, `{"name":"Accts","fiscal_year_end":"12-31"}`)
	o := `"org_id":` + itoa(org)
	id := func(m map[string]any) int64 { return int64(m["id"].(float64)) }
	post := func(path, body string, status int) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "POST", "/api/v1/"+path, `{`+o+`,`+body+`}`, owner), status)
	}
	plans := id(post("accounts", `"code":"4100","name":"Plan income","type":"revenue"`, 201))
	setup := id(post("accounts", `"code":"4200","name":"Setup income","type":"revenue"`, 201))
	cust := id(post("contacts", `"role":"customer","name":"Subscriber"`, 201))
	prod := id(post("billing/products", `"name":"Cloud"`, 201))
	plan := id(post("billing/plans", `"product_id":`+itoa(prod)+`,"name":"Basic","code":"basic","interval_count":1,"interval_unit":"month","price_cents":1000,"setup_fee_cents":500,"item_type":"service","plan_account_id":`+itoa(plans)+`,"setup_fee_account_id":`+itoa(setup), 201))
	post("billing/subscriptions", `"contact_id":`+itoa(cust)+`,"mode":"plan","plan_id":`+itoa(plan)+`,"quantity":1,"start_date":"2026-01-31"`, 201)
	ids := post("billing/subscriptions/run", `"as_of":"2026-01-31"`, 200)["created_invoice_ids"].([]any)
	inv := int64(ids[0].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+itoa(org), "", owner), 200)
	got := map[int64]int64{}
	rows, err := pool.Query(t.Context(), `SELECT l.account_id,l.credit_cents FROM journal_lines l JOIN invoices i ON i.journal_id=l.journal_id WHERE i.id=$1 AND l.credit_cents>0`, inv)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var acct, c int64
		if err := rows.Scan(&acct, &c); err != nil {
			t.Fatal(err)
		}
		got[acct] = c
	}
	rows.Close()
	if len(got) != 2 || got[plans] != 1000 || got[setup] != 500 {
		t.Fatalf("income legs=%v", got)
	}
}
