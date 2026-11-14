//go:build sampleseed

// Run only on purpose: go test -tags sampleseed -run TestSeedSample -count=1 .
// Seeds one explicitly marked shared, read-only sample company. Journals are posted directly; invoices and bills are not created
// through the app because GST-registered companies are refused there. Refuses to run twice.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sampleShared = "SAMPLE - Demo Trading Pte Ltd (not a real company)"

type sampleLine struct {
	code          string
	debit, credit int64
}

type sampleJournal struct {
	key   string
	date  time.Time
	memo  string
	lines []sampleLine
}

func TestSeedSample(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// Serialize seed attempts, including legacy unmarked samples, in the same transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(736427191)`); err != nil {
		t.Fatal(err)
	}
	var existing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizations WHERE is_shared_sample OR name=$1)`, sampleShared).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing {
		t.Fatal("sample exists; inspect it and follow operator setup instead of seeding again")
	}
	rows, err := tx.Query(ctx, `SELECT id FROM users ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		t.Fatal("no users; create one login first")
	}

	sharedID := seedSampleOrg(ctx, t, tx, sampleShared, ids[0])
	for _, uid := range ids[1:] {
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'viewer')`, sharedID, uid); err != nil {
			t.Fatal(err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded shared sample org %d for %d users", sharedID, len(ids))
}

func seedSampleOrg(ctx context.Context, t *testing.T, tx pgx.Tx, name string, owner int64) int64 {
	var orgID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (name, uen, owner_user_id, fiscal_year_end, gst_registered, is_shared_sample)
		 VALUES ($1, '', $2, '12-31', true, true) RETURNING id`, name, owner).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, owner); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct{ code, name, typ string }{
		{"2300", "GST Output", "liability"},
		{"2310", "GST Input", "asset"},
		{"6100", "Rent", "expense"},
		{"6300", "Supplies and services", "expense"},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO accounts (org_id, code, name, type, class) VALUES ($1, $2, $3, $4, $5)`, orgID, a.code, a.name, a.typ, defaultAccountClass(a.typ, a.code)); err != nil {
			t.Fatal(err)
		}
	}
	acct, err := accountIDs(ctx, tx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range sampleYear(orgID) {
		if err := postSampleJournal(ctx, tx, orgID, owner, acct, j); err != nil {
			t.Fatalf("%s: %v", j.key, err)
		}
	}
	var dr, cr int64
	var n int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(debit_cents),0), COALESCE(sum(credit_cents),0), count(*) FROM journal_lines WHERE org_id=$1`, orgID).Scan(&dr, &cr, &n); err != nil {
		t.Fatal(err)
	}
	if dr != cr {
		t.Fatalf("trial balance does not balance: debit %d credit %d", dr, cr)
	}
	t.Logf("org %d %q: %d journal lines, debit=credit=%d", orgID, name, n, dr)
	return orgID
}

// sampleYear builds calendar 2025: opening balance, monthly sales, receipts, purchases, rent and quarterly GST payments.
func sampleYear(orgID int64) []sampleJournal {
	d := func(m, day int) time.Time { return time.Date(2025, time.Month(m), day, 0, 0, 0, 0, time.UTC) }
	k := func(s string) string { return fmt.Sprintf("sample-%d-%s", orgID, s) }
	var out []sampleJournal
	out = append(out, sampleJournal{key: k("opening"), date: d(1, 1), memo: "Opening balance (SAMPLE)",
		lines: []sampleLine{{"1000", 5000000, 0}, {"3900", 0, 5000000}}})
	const net, gst, purchase, purchaseGST, rent = 2000000, 180000, 500000, 45000, 300000
	var outputQ, inputQ int64
	for m := 1; m <= 12; m++ {
		out = append(out, sampleJournal{key: k(fmt.Sprintf("sale-%02d", m)), date: d(m, 15), memo: "Sales invoices (SAMPLE)",
			lines: []sampleLine{{"1100", net + gst, 0}, {"4000", 0, net}, {"2300", 0, gst}}})
		out = append(out, sampleJournal{key: k(fmt.Sprintf("receipt-%02d", m)), date: d(m, 20), memo: "Customer receipts (SAMPLE)",
			lines: []sampleLine{{"1000", net + gst, 0}, {"1100", 0, net + gst}}})
		out = append(out, sampleJournal{key: k(fmt.Sprintf("purchase-%02d", m)), date: d(m, 22), memo: "Supplier payments (SAMPLE)",
			lines: []sampleLine{{"6300", purchase, 0}, {"2310", purchaseGST, 0}, {"1000", 0, purchase + purchaseGST}}})
		out = append(out, sampleJournal{key: k(fmt.Sprintf("rent-%02d", m)), date: d(m, 1), memo: "Rent (SAMPLE)",
			lines: []sampleLine{{"6100", rent, 0}, {"1000", 0, rent}}})
		outputQ += gst
		inputQ += purchaseGST
		if m%3 == 0 {
			pay := outputQ - inputQ
			out = append(out, sampleJournal{key: k(fmt.Sprintf("gst-%02d", m)), date: d(m, 28), memo: "GST payment to IRAS (SAMPLE)",
				lines: []sampleLine{{"2300", outputQ, 0}, {"2310", 0, inputQ}, {"1000", 0, pay}}})
			outputQ, inputQ = 0, 0
		}
	}
	return out
}

func postSampleJournal(ctx context.Context, tx pgx.Tx, orgID, userID int64, acct map[string]int64, j sampleJournal) error {
	var dr, cr int64
	for _, l := range j.lines {
		dr += l.debit
		cr += l.credit
	}
	if dr != cr {
		return fmt.Errorf("unbalanced %d/%d", dr, cr)
	}
	hash := sha256.Sum256([]byte(j.key))
	var jid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, j.date, j.memo, j.key, hash[:], userID).Scan(&jid); err != nil {
		return err
	}
	for i, l := range j.lines {
		id, ok := acct[l.code]
		if !ok {
			return fmt.Errorf("no account %s", l.code)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents) VALUES ($1,$2,$3,$4,$5,$6)`,
			orgID, jid, i+1, id, l.debit, l.credit); err != nil {
			return err
		}
	}
	return nil
}

// Operator step for a sample seeded before is_shared_sample existed. Names confer no trust, so mark by explicit id:
// SAMPLE_ORG_ID=<id> go test -tags sampleseed -run TestMarkExistingSample -count=1 .
func TestMarkExistingSample(t *testing.T) {
	url, id := os.Getenv("TEST_DATABASE_URL"), os.Getenv("SAMPLE_ORG_ID")
	if url == "" || id == "" {
		t.Skip("TEST_DATABASE_URL and SAMPLE_ORG_ID required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tag, err := pool.Exec(ctx, `UPDATE organizations SET is_shared_sample=true WHERE id=$1::bigint AND parent_company_id IS NULL AND status='active'`, id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("not marked (need an active company id; only one sample allowed): %v", err)
	}
}
