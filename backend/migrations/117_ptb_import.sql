-- Peachtree .ptb import: an upload is staged per company, keyed by the file hash so the same
-- backup cannot be imported twice. Nothing here touches the books; posting is a later step.
CREATE TABLE ptb_imports (
  id            bigserial PRIMARY KEY,
  org_id        bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  created_by    bigint REFERENCES users(id) ON DELETE SET NULL,
  filename      text NOT NULL,
  sha256        text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes    bigint NOT NULL CHECK (size_bytes > 0),
  status        text NOT NULL DEFAULT 'staged'
                  CHECK (status IN ('staged', 'previewed', 'posted', 'failed')),
  record_counts jsonb NOT NULL DEFAULT '{}'::jsonb,
  error         text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, sha256)
);

-- One row per decoded record, kept verbatim so later steps can be re-run without the upload.
CREATE TABLE ptb_import_records (
  import_id  bigint NOT NULL REFERENCES ptb_imports(id) ON DELETE CASCADE,
  table_name text NOT NULL,
  ordinal    int NOT NULL,
  payload    bytea NOT NULL,
  PRIMARY KEY (import_id, table_name, ordinal)
);

-- Rows and tables the importer refused, kept with the upload so the preview can list them.
ALTER TABLE ptb_imports ADD COLUMN exceptions jsonb NOT NULL DEFAULT '[]'::jsonb;
-- Last reconciliation result, so a commit can refuse a mismatched import.
ALTER TABLE ptb_imports ADD COLUMN reconciliation jsonb;
