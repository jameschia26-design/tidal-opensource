-- Recurring expense profiles. A run posts one real paid expense per due date; nothing runs on its own.
CREATE TABLE recurring_expense_profiles (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  repeat_every integer NOT NULL CHECK (repeat_every BETWEEN 1 AND 12),
  repeat_unit text NOT NULL CHECK (repeat_unit IN ('week','month','year')),
  start_date date NOT NULL,
  ends_on date CHECK (ends_on IS NULL OR ends_on >= start_date),
  next_run date NOT NULL,
  runs_done integer NOT NULL DEFAULT 0,
  expense_account_id bigint NOT NULL,
  paid_through_account_id bigint NOT NULL,
  customer_id bigint,
  payee text NOT NULL CHECK (length(trim(payee)) BETWEEN 1 AND 200),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  active boolean NOT NULL DEFAULT true,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, expense_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, paid_through_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, customer_id) REFERENCES contacts (org_id, id)
);
ALTER TABLE expenses ADD COLUMN recurring_profile_id bigint;
ALTER TABLE expenses ADD COLUMN recurring_run_date date;
ALTER TABLE expenses ADD CONSTRAINT expenses_recurring_run_uq UNIQUE (org_id, recurring_profile_id, recurring_run_date);
ALTER TABLE expenses ADD FOREIGN KEY (org_id, recurring_profile_id) REFERENCES recurring_expense_profiles (org_id, id);
