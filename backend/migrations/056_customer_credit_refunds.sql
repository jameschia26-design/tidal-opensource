-- Paying money back to a customer against an open credit note. Posts Dr Accounts Receivable, Cr Bank.
-- Append-only; idempotency key per company like payments.
CREATE TABLE customer_credit_refunds (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  credit_note_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  refund_date date NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 90),
  request_hash bytea NOT NULL,
  journal_id bigint NOT NULL REFERENCES journals(id),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  CONSTRAINT customer_credit_refunds_idempotency_uq UNIQUE (org_id, idempotency_key),
  FOREIGN KEY (org_id, credit_note_id) REFERENCES customer_credit_notes (org_id, id)
);
CREATE TRIGGER customer_credit_refunds_append_only BEFORE UPDATE OR DELETE ON customer_credit_refunds
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
