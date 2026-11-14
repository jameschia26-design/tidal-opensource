package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"tidal/backend/internal/ptb"
)

// These are synthetic Peachtree documents, not private data: each case proves one allocation rule
// from the importer's settlement handling.

func ptbTestBill(post uint32, contact, date, ref string, amount int64) ptbDoc {
	return ptbDoc{PostOrder: post, Kind: ptb.Bill, Contact: contact, Party: 1, Date: date, Ref: ref, Amount: amount}
}

func ptbTestPayment(post uint32, contact, date string, amount int64, links ...ptbLink) ptbDoc {
	return ptbDoc{PostOrder: post, Kind: ptb.Payment, Contact: contact, Party: 1, Date: date, Amount: amount, Links: links}
}

type ptbBillState struct {
	Remaining int64
	Paid      int64
	PaidOn    string
}

func recordPTBSynthetic(t *testing.T, a *app, org, userID, sales, journal int64, docs []ptbDoc, asOf string) ptbDocCounts {
	t.Helper()
	ctx := context.Background()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	journals := map[uint32]int64{}
	for _, d := range docs {
		journals[d.PostOrder] = journal
	}
	counts, err := recordPTBDocuments(ctx, tx, org, userID, strings.Repeat("a", 64), docs, journals, sales, map[uint64]int64{}, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return counts
}

func ptbBillStates(t *testing.T, a *app, org int64) map[string]ptbBillState {
	t.Helper()
	rows, err := a.db.Query(context.Background(), `SELECT b.supplier_reference,
		b.total_cents - COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id),0),
		COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id),0),
		COALESCE((SELECT max(p.payment_date)::text FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id),'')
		FROM bills b WHERE b.org_id=$1 ORDER BY b.supplier_reference`, org)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]ptbBillState{}
	for rows.Next() {
		var ref string
		var s ptbBillState
		if err := rows.Scan(&ref, &s.Remaining, &s.Paid, &s.PaidOn); err != nil {
			t.Fatal(err)
		}
		out[ref] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPTBPaymentAllocation covers the settlement rules: every link is used for its own amount, a
// payment may settle a later bill but never one after the report date, leftovers are retried, and an
// old unlinked payment never consumes a much later bill.
func TestPTBPaymentAllocation(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-alloc@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB allocation","fiscal_year_end":"09-30"}`)
	o := itoa(org)
	expense := accID(t, h, `"6501"`, o, owner, "expense")
	bank := accID(t, h, `"1010"`, o, owner, "asset")
	journal := int64(expectStatus(t, do(t, h, "POST", "/api/v1/journals",
		`{"org_id":`+o+`,"entry_date":"2026-01-01","memo":"PTB allocation fixture","idempotency_key":"ptb-alloc","lines":[{"account_id":`+expense+`,"debit_cents":1},{"account_id":`+bank+`,"credit_cents":1}]}`, owner), 201)["id"].(float64))
	var userID int64
	if err := a.db.QueryRow(context.Background(), `SELECT id FROM users WHERE email=$1`, "ptb-alloc@example.com").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	run := func(docs []ptbDoc, asOf string) ptbDocCounts {
		return recordPTBSynthetic(t, a, org, userID, mustInt64(t, expense), journal, docs, asOf)
	}

	t.Run("payment dated before the bill it settles", func(t *testing.T) {
		counts := run([]ptbDoc{
			ptbTestBill(1, "Alpha", "2026-03-10", "A-1", 10000),
			ptbTestPayment(2, "Alpha", "2026-03-01", 10000, ptbLink{Target: 1, Amount: 10000}),
		}, "2026-03-31")
		got := ptbBillStates(t, a, org)["A-1"]
		if got.Paid != 10000 || got.Remaining != 0 || got.PaidOn != "2026-03-10" {
			t.Fatalf("A-1 = %+v", got)
		}
		if counts.Payments != 1 || counts.DateAdjusted != 1 || counts.Skipped != 0 {
			t.Fatalf("counts = %+v", counts)
		}
	})

	t.Run("payment split across two bills uses each link amount", func(t *testing.T) {
		run([]ptbDoc{
			ptbTestBill(3, "Beta", "2026-02-01", "B-1", 6000),
			ptbTestBill(4, "Beta", "2026-02-05", "B-2", 4000),
			ptbTestPayment(5, "Beta", "2026-02-10", 10000, ptbLink{Target: 3, Amount: 6000}, ptbLink{Target: 4, Amount: 4000}),
		}, "2026-02-28")
		states := ptbBillStates(t, a, org)
		if states["B-1"] != (ptbBillState{Remaining: 0, Paid: 6000, PaidOn: "2026-02-10"}) ||
			states["B-2"] != (ptbBillState{Remaining: 0, Paid: 4000, PaidOn: "2026-02-10"}) {
			t.Fatalf("B-1 %+v B-2 %+v", states["B-1"], states["B-2"])
		}
	})

	t.Run("payment with no usable link falls back to an earlier bill", func(t *testing.T) {
		run([]ptbDoc{
			ptbTestBill(6, "Gamma", "2026-01-05", "G-1", 5000),
			ptbTestPayment(7, "Gamma", "2026-01-20", 5000, ptbLink{Target: 999, Amount: 5000}),
		}, "2026-01-31")
		got := ptbBillStates(t, a, org)["G-1"]
		if got.Paid != 5000 || got.Remaining != 0 || got.PaidOn != "2026-01-20" {
			t.Fatalf("G-1 = %+v", got)
		}
	})

	t.Run("leftover is retried dated at the bill", func(t *testing.T) {
		counts := run([]ptbDoc{
			ptbTestBill(8, "Delta", "2026-01-05", "D-1", 6000),
			ptbTestBill(9, "Delta", "2026-02-10", "D-2", 6000),
			ptbTestPayment(10, "Delta", "2026-01-20", 10000, ptbLink{Target: 8, Amount: 6000}),
		}, "2026-02-28")
		states := ptbBillStates(t, a, org)
		if states["D-1"] != (ptbBillState{Remaining: 0, Paid: 6000, PaidOn: "2026-01-20"}) ||
			states["D-2"] != (ptbBillState{Remaining: 2000, Paid: 4000, PaidOn: "2026-02-10"}) {
			t.Fatalf("D-1 %+v D-2 %+v", states["D-1"], states["D-2"])
		}
		if counts.DateAdjusted != 1 {
			t.Fatalf("counts = %+v", counts)
		}
	})

	t.Run("old unlinked payment does not touch a later bill", func(t *testing.T) {
		counts := run([]ptbDoc{
			ptbTestBill(11, "Epsilon", "2026-03-10", "E-1", 5000),
			ptbTestPayment(12, "Epsilon", "2022-01-10", 5000),
		}, "2026-03-31")
		got := ptbBillStates(t, a, org)["E-1"]
		if got.Paid != 0 || got.Remaining != 5000 {
			t.Fatalf("E-1 = %+v", got)
		}
		if counts.Skipped != 1 {
			t.Fatalf("counts = %+v", counts)
		}
	})

	t.Run("payment and bills never pass the as-of date", func(t *testing.T) {
		counts := run([]ptbDoc{
			ptbTestBill(13, "Zeta", "2026-03-10", "Z-1", 5000),
			ptbTestBill(14, "Zeta", "2026-04-10", "Z-2", 5000),
			ptbTestPayment(15, "Zeta", "2026-04-05", 10000, ptbLink{Target: 13, Amount: 5000}, ptbLink{Target: 14, Amount: 5000}),
		}, "2026-03-31")
		states := ptbBillStates(t, a, org)
		// The payment is dated 5 April, after the report date, so it settles nothing at 31 March.
		if states["Z-1"] != (ptbBillState{Remaining: 5000, Paid: 0, PaidOn: ""}) {
			t.Fatalf("Z-1 = %+v", states["Z-1"])
		}
		if states["Z-2"] != (ptbBillState{Remaining: 5000, Paid: 0, PaidOn: ""}) {
			t.Fatalf("Z-2 = %+v", states["Z-2"])
		}
		if counts.Skipped != 1 {
			t.Fatalf("counts = %+v", counts)
		}
	})
}

func mustInt64(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
