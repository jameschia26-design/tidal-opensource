-- Repeating journals. A run posts one real journal per due date; nothing runs on its own.
CREATE TABLE repeating_journal_profiles (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  narration text NOT NULL CHECK (length(trim(narration)) BETWEEN 1 AND 500),
  repeat_every integer NOT NULL CHECK (repeat_every BETWEEN 1 AND 12),
  repeat_unit text NOT NULL CHECK (repeat_unit IN ('week','month','year')),
  start_date date NOT NULL,
  ends_on date CHECK (ends_on IS NULL OR ends_on >= start_date),
  next_run date NOT NULL,
  runs_done integer NOT NULL DEFAULT 0,
  active boolean NOT NULL DEFAULT true,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE TABLE repeating_journal_lines (
  org_id bigint NOT NULL,
  profile_id bigint NOT NULL,
  line_no integer NOT NULL CHECK (line_no BETWEEN 1 AND 200),
  account_id bigint NOT NULL,
  debit_cents bigint NOT NULL DEFAULT 0 CHECK (debit_cents BETWEEN 0 AND 100000000000000),
  credit_cents bigint NOT NULL DEFAULT 0 CHECK (credit_cents BETWEEN 0 AND 100000000000000),
  PRIMARY KEY (org_id, profile_id, line_no),
  CHECK ((debit_cents = 0) <> (credit_cents = 0)),
  FOREIGN KEY (org_id, profile_id) REFERENCES repeating_journal_profiles (org_id, id),
  FOREIGN KEY (org_id, account_id) REFERENCES accounts (org_id, id)
);
