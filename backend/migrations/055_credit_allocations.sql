-- Applying an open customer credit note to a posted invoice. No journal entry: opening the credit already reduced receivables.
-- Append-only; a row is one slice of the credit applied to one invoice.
CREATE TABLE customer_credit_applications (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  credit_note_id bigint NOT NULL,
  invoice_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  applied_on date NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, credit_note_id) REFERENCES customer_credit_notes (org_id, id),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices (org_id, id)
);
CREATE TRIGGER customer_credit_applications_append_only BEFORE UPDATE OR DELETE ON customer_credit_applications
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
