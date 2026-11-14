-- Supplier credits reduce a posted bill: they reverse the bill's expense and input GST and lower what is owed,
-- and can be refunded to the bank. Linked to the bill by default (D7).
CREATE TABLE supplier_credits (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  bill_id bigint NOT NULL,
  contact_id bigint NOT NULL,
  credit_date date NOT NULL,
  reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 500),
  net_cents bigint NOT NULL CHECK (net_cents >= 0),
  gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0),
  total_cents bigint NOT NULL CHECK (total_cents > 0),
  journal_id bigint NOT NULL,
  refunded_on date,
  refund_journal_id bigint,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, bill_id) REFERENCES bills (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id),
  CHECK (total_cents = net_cents + gst_cents)
);
CREATE INDEX supplier_credits_bill ON supplier_credits(org_id, bill_id);
