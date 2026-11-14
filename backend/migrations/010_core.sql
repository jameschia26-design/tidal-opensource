-- Core accounting and CRM: invoices (draft -> posted), payments, leads and deals.
-- Money is bigint cents. Every row carries org_id; links between rows use compound foreign keys
-- so a row cannot point at another organization's data.

-- Gapless invoice numbering per organization, advanced under row lock when an invoice posts.
CREATE TABLE core_counters (
  org_id       bigint PRIMARY KEY REFERENCES organizations(id),
  invoice_next bigint NOT NULL DEFAULT 1 CHECK (invoice_next >= 1)
);

CREATE TABLE invoices (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES organizations(id),
  contact_id   bigint NOT NULL,
  number       bigint,
  status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'posted')),
  invoice_date date NOT NULL,
  due_date     date NOT NULL CHECK (due_date >= invoice_date),
  total_cents  bigint NOT NULL CHECK (total_cents > 0 AND total_cents <= 100000000000000),
  journal_id   bigint,
  created_by   bigint NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  posted_at    timestamptz,
  UNIQUE (org_id, id),
  UNIQUE (org_id, number),
  CHECK ((status = 'draft') = (number IS NULL AND journal_id IS NULL AND posted_at IS NULL)),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE INDEX invoices_org_status_idx ON invoices (org_id, status);

CREATE TABLE invoice_lines (
  id               bigserial PRIMARY KEY,
  org_id           bigint NOT NULL,
  invoice_id       bigint NOT NULL,
  line_no          int NOT NULL CHECK (line_no > 0),
  description      text NOT NULL CHECK (length(trim(description)) > 0 AND length(description) <= 200),
  quantity         bigint NOT NULL CHECK (quantity > 0 AND quantity <= 1000000),
  unit_price_cents bigint NOT NULL CHECK (unit_price_cents >= 0),
  amount_cents     bigint NOT NULL CHECK (amount_cents >= 0 AND amount_cents = quantity * unit_price_cents),
  UNIQUE (invoice_id, line_no),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices (org_id, id)
);

CREATE TABLE payments (
  id              bigserial PRIMARY KEY,
  org_id          bigint NOT NULL REFERENCES organizations(id),
  invoice_id      bigint NOT NULL,
  amount_cents    bigint NOT NULL CHECK (amount_cents > 0),
  payment_date    date NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 90),
  request_hash    bytea NOT NULL,
  journal_id      bigint NOT NULL,
  created_by      bigint NOT NULL REFERENCES users(id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  CONSTRAINT payments_idempotency_uq UNIQUE (org_id, idempotency_key),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE INDEX payments_org_invoice_idx ON payments (org_id, invoice_id);

CREATE TABLE leads (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL REFERENCES organizations(id),
  name       text NOT NULL CHECK (length(trim(name)) > 0 AND length(name) <= 200),
  email      text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
  source     text NOT NULL DEFAULT '' CHECK (length(source) <= 100),
  stage      text NOT NULL DEFAULT 'new' CHECK (stage IN ('new', 'contacted', 'qualified', 'lost')),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);

CREATE TABLE deals (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES organizations(id),
  lead_id      bigint,
  title        text NOT NULL CHECK (length(trim(title)) > 0 AND length(title) <= 200),
  amount_cents bigint NOT NULL CHECK (amount_cents >= 0 AND amount_cents <= 100000000000000),
  stage        text NOT NULL DEFAULT 'open' CHECK (stage IN ('open', 'won', 'lost')),
  created_by   bigint NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, lead_id) REFERENCES leads (org_id, id)
);

-- Append-only: invoice lines, payments and invoice deletes are never changed.
CREATE TRIGGER invoice_lines_append_only BEFORE UPDATE OR DELETE ON invoice_lines
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
CREATE TRIGGER payments_append_only BEFORE UPDATE OR DELETE ON payments
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
CREATE TRIGGER invoices_no_delete BEFORE DELETE ON invoices
  FOR EACH ROW EXECUTE FUNCTION forbid_change();

-- A posted invoice is immutable. A draft may change only to posted, which sets its number,
-- journal and posting time; amounts, parties and dates cannot move.
CREATE FUNCTION invoice_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'posted' THEN
    RAISE EXCEPTION 'posted invoice % is immutable', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status <> 'posted' OR NEW.number IS NULL OR NEW.journal_id IS NULL OR NEW.posted_at IS NULL
     OR NEW.org_id <> OLD.org_id OR NEW.contact_id <> OLD.contact_id OR NEW.total_cents <> OLD.total_cents
     OR NEW.invoice_date <> OLD.invoice_date OR NEW.due_date <> OLD.due_date THEN
    RAISE EXCEPTION 'draft invoice % may only change to posted with its number and journal', OLD.id
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER invoices_guard BEFORE UPDATE ON invoices
  FOR EACH ROW EXECUTE FUNCTION invoice_guard();

-- Lines must exist and sum to the stored invoice total, checked at commit.
CREATE FUNCTION check_invoice_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  s bigint;
  n int;
  t bigint;
BEGIN
  SELECT COALESCE(SUM(amount_cents), 0), COUNT(*) INTO s, n
    FROM invoice_lines WHERE invoice_id = NEW.invoice_id;
  SELECT total_cents INTO t FROM invoices WHERE id = NEW.invoice_id;
  IF n < 1 OR s <> t THEN
    RAISE EXCEPTION 'invoice % lines (%) do not sum to its total (%)', NEW.invoice_id, s, t
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER invoice_lines_total AFTER INSERT ON invoice_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_invoice_total();

-- Payments only against posted invoices, and never beyond the invoice total, checked at commit.
CREATE FUNCTION check_payments_within_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  s bigint;
  t bigint;
  st text;
BEGIN
  SELECT total_cents, status INTO t, st FROM invoices WHERE id = NEW.invoice_id;
  SELECT COALESCE(SUM(amount_cents), 0) INTO s FROM payments WHERE invoice_id = NEW.invoice_id;
  IF st <> 'posted' OR s > t THEN
    RAISE EXCEPTION 'invoice % cannot take payments totalling % (total %, status %)', NEW.invoice_id, s, t, st
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER payments_within_total AFTER INSERT ON payments
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_payments_within_total();
