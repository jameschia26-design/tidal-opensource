-- Budgets: one amount per account per month. Budget reports read these rows against posted actuals.
CREATE TABLE budgets (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  period_month date NOT NULL CHECK (extract(day from period_month) = 1),
  account_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN -100000000000000 AND 100000000000000),
  updated_by bigint NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, period_month, account_id),
  FOREIGN KEY (org_id, account_id) REFERENCES accounts(org_id, id)
);
CREATE INDEX budgets_month_idx ON budgets(org_id, period_month);
