-- Hourly cost rate per staff member (SGD cents). Used for project cost reporting only; never posts to the ledger.
CREATE TABLE staff_cost_rates (
  org_id bigint NOT NULL REFERENCES organizations(id),
  user_id bigint NOT NULL REFERENCES users(id),
  hourly_cents bigint NOT NULL CHECK (hourly_cents BETWEEN 0 AND 100000000),
  updated_by bigint NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);
