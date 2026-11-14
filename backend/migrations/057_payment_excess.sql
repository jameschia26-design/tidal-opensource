-- Money a customer paid beyond the invoices it settled. The receipt's journal already credited Accounts Receivable
-- for the full amount, so the surplus is an open customer credit with no extra journal. Append-only.
CREATE TABLE payment_excess (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  payment_id bigint NOT NULL,
  contact_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  excess_date date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, payment_id),
  FOREIGN KEY (org_id, payment_id) REFERENCES payments_received (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id)
);
CREATE TRIGGER payment_excess_append_only BEFORE UPDATE OR DELETE ON payment_excess
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
