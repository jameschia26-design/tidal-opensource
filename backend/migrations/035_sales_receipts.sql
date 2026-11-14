-- Sales receipts: a sale paid at once. Saving posts Dr deposit account, Cr income; no receivable is created. Rows are never edited.
CREATE TABLE sales_receipts (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  receipt_number text NOT NULL CHECK (length(trim(receipt_number)) BETWEEN 1 AND 50),
  receipt_date date NOT NULL,
  lines jsonb NOT NULL,
  commercial jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(commercial)='object'),
  total_cents bigint NOT NULL CHECK (total_cents BETWEEN 1 AND 100000000000000),
  payment_mode text NOT NULL CHECK (payment_mode IN ('cash','bank_transfer','cheque','card','paynow','other')),
  deposit_account_id bigint NOT NULL,
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  terms text NOT NULL DEFAULT '' CHECK (length(terms) <= 4000),
  journal_id bigint NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 100),
  request_hash bytea NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, receipt_number),
  UNIQUE (org_id, idempotency_key),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, deposit_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id),
  CHECK (valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) = total_cents)
);
CREATE TRIGGER sales_receipts_append_only BEFORE UPDATE OR DELETE ON sales_receipts FOR EACH ROW EXECUTE FUNCTION forbid_change();
