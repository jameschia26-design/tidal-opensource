-- R16: statements belong to a chosen bank account, one line can cover several documents (a grouped deposit),
-- matching has a tolerance, and accounts carry an explicit bank flag instead of a code range.
ALTER TABLE accounts ADD COLUMN is_bank_account boolean NOT NULL DEFAULT false;
UPDATE accounts SET is_bank_account=true WHERE code='1000';
ALTER TABLE bank_statement_lines ADD COLUMN bank_account_id bigint REFERENCES accounts(id);
UPDATE bank_statement_lines l SET bank_account_id=a.id FROM accounts a WHERE a.org_id=l.org_id AND a.code='1000';
ALTER TABLE organizations ADD COLUMN bank_match_tolerance_days int NOT NULL DEFAULT 0
  CHECK (bank_match_tolerance_days BETWEEN 0 AND 31);

CREATE TABLE bank_line_matches (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  line_id bigint NOT NULL REFERENCES bank_statement_lines(id),
  doc_kind text NOT NULL CHECK (doc_kind IN ('invoice','bill','expense','receipt','transfer')),
  doc_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents <> 0),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, doc_kind, doc_id)
);
CREATE INDEX bank_line_matches_line ON bank_line_matches (org_id, line_id);
