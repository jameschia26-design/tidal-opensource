CREATE TABLE mileage_preferences (
  org_id bigint PRIMARY KEY REFERENCES organizations(id),
  expense_account_id bigint NOT NULL,
  unit text NOT NULL CHECK(unit IN ('km','mile')),
  default_rate_cents bigint NOT NULL CHECK(default_rate_cents BETWEEN 0 AND 100000000),
  rates jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(rates)='array'),
  FOREIGN KEY(org_id,expense_account_id) REFERENCES accounts(org_id,id)
);
ALTER TABLE expenses ADD COLUMN mileage jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(mileage)='object');
