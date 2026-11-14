package main

import (
	"os"
	"strings"
	"testing"
)

// The entity backfill rewrites append-only audit rows, which only works on a database that already has
// events. A fresh migration run has none, so this exercises the migration against a populated table.
func TestAuditEntityBackfillOnExistingRows(t *testing.T) {
	_, db := testApp(t)
	ctx := t.Context()
	if _, err := db.Exec(ctx, `CREATE SCHEMA backfill_probe`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(ctx, `DROP SCHEMA backfill_probe CASCADE`)
	if _, err := db.Exec(ctx, `
		CREATE TABLE backfill_probe.audit_events (
			id bigserial PRIMARY KEY, org_id bigint, user_id bigint, action text NOT NULL,
			detail jsonb NOT NULL DEFAULT '{}'::jsonb, entity_kind text, entity_id bigint,
			created_at timestamptz NOT NULL DEFAULT now());
		CREATE FUNCTION backfill_probe.forbid_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION '% on % is not allowed', TG_OP, TG_TABLE_NAME USING ERRCODE='insufficient_privilege'; END $$;
		CREATE TRIGGER audit_events_append_only BEFORE UPDATE OR DELETE ON backfill_probe.audit_events
			FOR EACH ROW EXECUTE FUNCTION backfill_probe.forbid_change();
		INSERT INTO backfill_probe.audit_events(action, detail) VALUES
			('invoice.posted', '{"invoice_id": 41}'),
			('bill.reversed', '{"reversal_journal_id": 9, "journal_id": 12}'),
			('login', '{"ip": "127.0.0.1"}');`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("migrations/116_audit_entity_historic.sql")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(string(raw), "UPDATE audit_events e", "UPDATE backfill_probe.audit_events e")
	body = strings.ReplaceAll(body, "FROM audit_events a", "FROM backfill_probe.audit_events a")
	body = strings.ReplaceAll(body, "ALTER TABLE audit_events", "ALTER TABLE backfill_probe.audit_events")
	if _, err := db.Exec(ctx, body); err != nil {
		t.Fatalf("backfill failed on existing rows: %v", err)
	}
	var kind string
	var id int64
	if err := db.QueryRow(ctx, `SELECT entity_kind, entity_id FROM backfill_probe.audit_events WHERE action='invoice.posted'`).Scan(&kind, &id); err != nil {
		t.Fatal(err)
	}
	if kind != "invoice" || id != 41 {
		t.Fatalf("invoice event not backfilled: %q %d", kind, id)
	}
	var journalID int64
	if err := db.QueryRow(ctx, `SELECT entity_id FROM backfill_probe.audit_events WHERE action='bill.reversed'`).Scan(&journalID); err != nil {
		t.Fatal(err)
	}
	if journalID != 12 {
		t.Fatalf("journal id should win over reversal_journal_id, got %d", journalID)
	}
	if _, err := db.Exec(ctx, `UPDATE backfill_probe.audit_events SET action='x' WHERE action='login'`); err == nil {
		t.Fatal("append-only guard was not restored after the backfill")
	}
}
