-- Bank rules suggest an account for imported statement lines whose description contains the rule text.
-- A rule never posts anything; it only suggests.
CREATE TABLE bank_rules (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  direction text NOT NULL CHECK (direction IN ('receive','spend')),
  contains_text text NOT NULL CHECK (length(trim(contains_text)) BETWEEN 1 AND 100),
  account_id bigint NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, account_id) REFERENCES accounts (org_id, id)
);
