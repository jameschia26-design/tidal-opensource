package main

import (
	"sync"
	"testing"
)

func TestPaymentDraftConcurrentLimit(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "draft-limit-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Draft limit","fiscal_year_end":"12-31"}`)
	_, err := pool.Exec(t.Context(), `INSERT INTO payment_drafts(org_id,side,data,created_by) SELECT $1,'received','{}'::jsonb,id FROM users CROSS JOIN generate_series(1,199) WHERE email='draft-limit-owner@example.com'`, org)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"org_id":` + itoa(org) + `,"side":"made","data":{}}`
	var wg sync.WaitGroup
	codes := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", "/api/v1/payment-drafts", body, owner).Code }()
	}
	wg.Wait()
	close(codes)
	created, rejected := 0, 0
	for code := range codes {
		if code == 201 {
			created++
		} else if code == 422 {
			rejected++
		} else {
			t.Fatal(code)
		}
	}
	if created != 1 || rejected != 4 {
		t.Fatal(created, rejected)
	}
}
