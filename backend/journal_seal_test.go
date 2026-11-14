package main

import "testing"

// TestJournalSealedAfterCommit covers B6: a committed journal cannot gain lines, and a journal with no
// lines cannot commit. Both were accepted before; the database now refuses them.
func TestJournalSealedAfterCommit(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "journal-seal@example.com")
	org := orgFromList(t, a, owner, `{"name":"Journal seal","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	rev := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4200","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64))
	m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-07-01","due_date":"2026-07-01","lines":[{"description":"Work","revenue_account_id":`+itoa(rev)+`,"quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	invID := int64(m["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+o, "", owner), 200)

	var jid, ar, sales int64
	if err := pool.QueryRow(t.Context(), `SELECT journal_id FROM invoices WHERE org_id=$1 AND id=$2`, org, invID).Scan(&jid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT
		(SELECT id FROM accounts WHERE org_id=$1 AND code='1100'),
		(SELECT id FROM accounts WHERE org_id=$1 AND code='4200')`, org).Scan(&ar, &sales); err != nil {
		t.Fatal(err)
	}

	// Probe 1: appending a balanced pair of lines to the committed journal must fail.
	tx, err := a.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err1 := tx.Exec(t.Context(), `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,901,$3,100,0)`, org, jid, ar)
	_, err2 := tx.Exec(t.Context(), `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,902,$3,0,100)`, org, jid, sales)
	commitErr := tx.Commit(t.Context())
	if err1 == nil && err2 == nil && commitErr == nil {
		t.Fatal("committed journal accepted appended lines")
	}
	if commitErr == nil {
		t.Fatal("append transaction committed despite a refused line")
	}

	// Probe 2: a journal with no lines must not commit.
	var uid int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM users WHERE email=$1`, "journal-seal@example.com").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	tx2, err := a.db.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx2.Exec(t.Context(), `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,'2026-07-02','empty','empty-probe','empty-hash',$2)`, org, uid); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(t.Context()); err == nil {
		t.Fatal("empty journal committed")
	}
	var empty int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1 AND idempotency_key='empty-probe'`, org).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if empty != 0 {
		t.Fatal("empty journal persisted")
	}
}
