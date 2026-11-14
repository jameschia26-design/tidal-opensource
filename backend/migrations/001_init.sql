-- Tidal initial schema. Money is integer cents (bigint). Company-owned rows carry org_id and
-- child links use compound foreign keys so a row cannot point at another organization's data.

CREATE TABLE users (
  id                bigserial PRIMARY KEY,
  email             text NOT NULL UNIQUE CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254),
  name              text NOT NULL DEFAULT '' CHECK (length(name) <= 120),
  password_hash     text,
  google_sub        text UNIQUE,
  tier              text NOT NULL DEFAULT 'essential' CHECK (tier IN ('essential', 'growth', 'premier')),
  -- Only the Google sign-in path may grant platform admin (see google.go); DB enforces it has a Google identity.
  is_platform_admin boolean NOT NULL DEFAULT false,
  disabled          boolean NOT NULL DEFAULT false,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (password_hash IS NOT NULL OR google_sub IS NOT NULL),
  CHECK (NOT is_platform_admin OR google_sub IS NOT NULL)
);

CREATE TABLE sessions (
  token_hash bytea PRIMARY KEY,
  user_id    bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE login_challenges (
  id         text PRIMARY KEY,
  user_id    bigint NOT NULL REFERENCES users(id),
  code_hash  text NOT NULL,
  attempts   int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);
CREATE INDEX login_challenges_user_idx ON login_challenges (user_id, created_at);

CREATE TABLE login_failures (
  id         bigserial PRIMARY KEY,
  email      text NOT NULL,
  ip         text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_email_idx ON login_failures (email, created_at);
CREATE INDEX login_failures_ip_idx ON login_failures (ip, created_at);

CREATE TABLE oauth_states (
  state         text PRIMARY KEY,
  nonce         text NOT NULL,
  code_verifier text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL
);

CREATE TABLE invitations (
  id          bigserial PRIMARY KEY,
  token_hash  bytea NOT NULL UNIQUE,
  email       text NOT NULL CHECK (email = lower(email)),
  tier        text NOT NULL DEFAULT 'essential' CHECK (tier IN ('essential', 'growth', 'premier')),
  org_id      bigint,
  role        text,
  created_by  bigint NOT NULL REFERENCES users(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  accepted_at timestamptz,
  CHECK ((org_id IS NULL) = (role IS NULL)),
  CHECK (role IS NULL OR role IN ('admin', 'accountant', 'member', 'viewer'))
);
CREATE INDEX invitations_email_idx ON invitations (email);

CREATE TABLE organizations (
  id            bigserial PRIMARY KEY,
  name          text NOT NULL CHECK (length(trim(name)) > 0 AND length(name) <= 200),
  uen           text NOT NULL DEFAULT '' CHECK (uen = '' OR uen ~ '^[A-Z0-9]{8,10}$'),
  owner_user_id bigint NOT NULL REFERENCES users(id),
  fiscal_year_end text NOT NULL DEFAULT '12-31' CHECK (fiscal_year_end ~ '^(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$'),
  gst_registered boolean NOT NULL DEFAULT false,
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organizations_owner_idx ON organizations (owner_user_id);

ALTER TABLE invitations ADD CONSTRAINT invitations_org_fk FOREIGN KEY (org_id) REFERENCES organizations(id);

CREATE TABLE memberships (
  org_id     bigint NOT NULL REFERENCES organizations(id),
  user_id    bigint NOT NULL REFERENCES users(id),
  role       text NOT NULL CHECK (role IN ('owner', 'admin', 'accountant', 'member', 'viewer')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);
CREATE INDEX memberships_user_idx ON memberships (user_id);

CREATE TABLE accounts (
  id     bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  code   text NOT NULL CHECK (length(trim(code)) > 0 AND length(code) <= 20),
  name   text NOT NULL CHECK (length(trim(name)) > 0),
  type   text NOT NULL CHECK (type IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
  UNIQUE (org_id, id),
  UNIQUE (org_id, code)
);

CREATE TABLE contacts (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL REFERENCES organizations(id),
  name       text NOT NULL CHECK (length(trim(name)) > 0 AND length(name) <= 200),
  email      text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
  kind       text NOT NULL CHECK (kind IN ('customer', 'supplier', 'both')),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);

CREATE TABLE journals (
  id              bigserial PRIMARY KEY,
  org_id          bigint NOT NULL REFERENCES organizations(id),
  entry_date      date NOT NULL,
  memo            text NOT NULL DEFAULT '' CHECK (length(memo) <= 500),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 100),
  request_hash    bytea NOT NULL,
  reversal_of     bigint,
  created_by      bigint NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  CONSTRAINT journals_idempotency_uq UNIQUE (org_id, idempotency_key),
  CONSTRAINT journals_one_reversal_uq UNIQUE (org_id, reversal_of),
  FOREIGN KEY (org_id, reversal_of) REFERENCES journals (org_id, id)
);

CREATE TABLE journal_lines (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL,
  journal_id   bigint NOT NULL,
  line_no      int NOT NULL CHECK (line_no > 0),
  account_id   bigint NOT NULL,
  debit_cents  bigint NOT NULL DEFAULT 0 CHECK (debit_cents >= 0),
  credit_cents bigint NOT NULL DEFAULT 0 CHECK (credit_cents >= 0),
  CHECK ((debit_cents = 0) <> (credit_cents = 0)),
  UNIQUE (journal_id, line_no),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id),
  FOREIGN KEY (org_id, account_id) REFERENCES accounts (org_id, id)
);
CREATE INDEX journal_lines_account_idx ON journal_lines (org_id, account_id);

CREATE TABLE audit_events (
  id         bigserial PRIMARY KEY,
  org_id     bigint REFERENCES organizations(id),
  user_id    bigint REFERENCES users(id),
  action     text NOT NULL,
  detail     jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only: posted journals, their lines and audit events cannot be changed or deleted.
CREATE FUNCTION forbid_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% on % is not allowed: records are append-only', TG_OP, TG_TABLE_NAME
    USING ERRCODE = 'insufficient_privilege';
END $$;

CREATE TRIGGER journals_append_only BEFORE UPDATE OR DELETE ON journals
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
CREATE TRIGGER journal_lines_append_only BEFORE UPDATE OR DELETE ON journal_lines
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
CREATE TRIGGER audit_events_append_only BEFORE UPDATE OR DELETE ON audit_events
  FOR EACH ROW EXECUTE FUNCTION forbid_change();

-- Every journal must have at least two lines and debits equal credits, checked at commit.
CREATE FUNCTION check_journal_balanced() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  d bigint;
  c bigint;
  n int;
BEGIN
  SELECT COALESCE(SUM(debit_cents), 0), COALESCE(SUM(credit_cents), 0), COUNT(*)
    INTO d, c, n
    FROM journal_lines WHERE journal_id = NEW.journal_id;
  IF n < 2 OR d <> c OR d = 0 THEN
    RAISE EXCEPTION 'journal % is not balanced (debits %, credits %, lines %)', NEW.journal_id, d, c, n
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER journal_lines_balanced AFTER INSERT ON journal_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_journal_balanced();
