-- Reversals of posted documents. Posted rows stay append-only, so the reversal is a mirrored journal plus a
-- row here: payments and expenses keep their history, and balances exclude whatever was reversed (R15).
CREATE TABLE document_reversals (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  doc_kind text NOT NULL CHECK (doc_kind IN ('bill','bill_payment','expense')),
  doc_id bigint NOT NULL,
  original_journal_id bigint NOT NULL,
  reversal_journal_id bigint NOT NULL,
  reversal_date date NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, doc_kind, doc_id),
  UNIQUE (org_id, reversal_journal_id),
  FOREIGN KEY (org_id, original_journal_id) REFERENCES journals (org_id, id),
  FOREIGN KEY (org_id, reversal_journal_id) REFERENCES journals (org_id, id)
);
