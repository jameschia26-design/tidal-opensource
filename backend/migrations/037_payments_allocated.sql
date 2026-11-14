-- Payments received (customer) and payments made (vendor): one receipt/payment settling several invoices/bills.
-- Each allocation also becomes a row in payments / bill_payments, so balances and aging keep working. Rows are never edited.
ALTER TABLE attachments DROP CONSTRAINT attachments_entity_kind_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_entity_kind_check CHECK (entity_kind IN
 ('contacts','invoices','quotes','bills','items','expenses','purchase-orders','purchase-receives','sales-receipts','credit-notes','billing-plans','billing-addons','payments-received','payments-made'));

CREATE TABLE payments_received (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  payment_number text NOT NULL CHECK (length(trim(payment_number)) BETWEEN 1 AND 50),
  payment_date date NOT NULL,
  payment_mode text NOT NULL CHECK (payment_mode IN ('cash','bank_transfer','cheque','card','paynow','other')),
  deposit_account_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  bank_charge_cents bigint NOT NULL DEFAULT 0 CHECK (bank_charge_cents >= 0 AND bank_charge_cents <= amount_cents),
  bank_charge_account_id bigint,
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  allocations jsonb NOT NULL CHECK (jsonb_typeof(allocations)='array' AND jsonb_array_length(allocations) BETWEEN 1 AND 200),
  journal_id bigint NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 100),
  request_hash bytea NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, payment_number),
  UNIQUE (org_id, idempotency_key),
  CHECK ((bank_charge_cents = 0) = (bank_charge_account_id IS NULL)),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, deposit_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, bank_charge_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE TRIGGER payments_received_append_only BEFORE UPDATE OR DELETE ON payments_received FOR EACH ROW EXECUTE FUNCTION forbid_change();

CREATE TABLE payments_made (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  payment_number text NOT NULL CHECK (length(trim(payment_number)) BETWEEN 1 AND 50),
  payment_date date NOT NULL,
  payment_mode text NOT NULL CHECK (payment_mode IN ('cash','bank_transfer','cheque','card','paynow','other')),
  paid_through_account_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  allocations jsonb NOT NULL CHECK (jsonb_typeof(allocations)='array' AND jsonb_array_length(allocations) BETWEEN 1 AND 200),
  journal_id bigint NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 100),
  request_hash bytea NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, payment_number),
  UNIQUE (org_id, idempotency_key),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, paid_through_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE TRIGGER payments_made_append_only BEFORE UPDATE OR DELETE ON payments_made FOR EACH ROW EXECUTE FUNCTION forbid_change();
