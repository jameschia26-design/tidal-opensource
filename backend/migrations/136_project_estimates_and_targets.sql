-- Xero projects carry estimated cost and charge; the scorecard targets are company settings.
ALTER TABLE projects
  ADD COLUMN estimated_cost_cents bigint NOT NULL DEFAULT 0 CHECK (estimated_cost_cents >= 0),
  ADD COLUMN estimated_charge_cents bigint NOT NULL DEFAULT 0 CHECK (estimated_charge_cents >= 0);
ALTER TABLE organizations
  ADD COLUMN scorecard_targets jsonb NOT NULL DEFAULT '{}'::jsonb;
