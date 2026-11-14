-- Paid expenses: money already spent, recorded against a bank account. Posted entries are append-only.
CREATE TABLE expenses (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  expense_date date NOT NULL,
  payee text NOT NULL CHECK (length(trim(payee)) BETWEEN 1 AND 200),
  description text NOT NULL CHECK (length(trim(description)) BETWEEN 1 AND 200),
  expense_account_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  receipt_reference text NOT NULL DEFAULT '' CHECK (length(receipt_reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  journal_id bigint NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, expense_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE TRIGGER expenses_append_only BEFORE UPDATE OR DELETE ON expenses
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
