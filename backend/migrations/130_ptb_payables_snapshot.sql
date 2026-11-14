-- Independently sourced, immutable open-item snapshot for a specific imported PTB and report date.
-- The composite keys below need a (org_id, id) target on the import table.
CREATE UNIQUE INDEX ptb_imports_org_id_id_key ON ptb_imports (org_id, id);
CREATE TABLE ptb_payables_snapshots (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL,
  import_id bigint NOT NULL,
  as_of date NOT NULL,
  source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
  row_count integer NOT NULL CHECK (row_count > 0),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, import_id) REFERENCES ptb_imports(org_id, id),
  UNIQUE (org_id, import_id, as_of)
);
CREATE TABLE ptb_payables_snapshot_rows (
  snapshot_id bigint NOT NULL REFERENCES ptb_payables_snapshots(id) ON DELETE CASCADE,
  party_id integer NOT NULL CHECK (party_id > 0),
  vendor_code text NOT NULL CHECK (length(trim(vendor_code)) BETWEEN 1 AND 40),
  party_name text NOT NULL CHECK (length(trim(party_name)) BETWEEN 1 AND 200),
  reference text NOT NULL CHECK (length(trim(reference)) BETWEEN 1 AND 100),
  bill_date date NOT NULL,
  due_date date NOT NULL,
  open_cents bigint NOT NULL CHECK (open_cents > 0),
  bucket_0_30_cents bigint NOT NULL CHECK (bucket_0_30_cents >= 0),
  bucket_31_60_cents bigint NOT NULL CHECK (bucket_31_60_cents >= 0),
  bucket_61_90_cents bigint NOT NULL CHECK (bucket_61_90_cents >= 0),
  bucket_over_90_cents bigint NOT NULL CHECK (bucket_over_90_cents >= 0),
  PRIMARY KEY (snapshot_id, party_id, reference),
  CHECK (open_cents = bucket_0_30_cents + bucket_31_60_cents + bucket_61_90_cents + bucket_over_90_cents)
);
CREATE FUNCTION forbid_ptb_payables_snapshot_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'PTB payable snapshots are immutable' USING ERRCODE='insufficient_privilege'; END $$;
CREATE TRIGGER ptb_payables_snapshots_immutable BEFORE UPDATE OR DELETE ON ptb_payables_snapshots FOR EACH ROW EXECUTE FUNCTION forbid_ptb_payables_snapshot_change();
CREATE TRIGGER ptb_payables_snapshot_rows_immutable BEFORE UPDATE OR DELETE ON ptb_payables_snapshot_rows FOR EACH ROW EXECUTE FUNCTION forbid_ptb_payables_snapshot_change();
