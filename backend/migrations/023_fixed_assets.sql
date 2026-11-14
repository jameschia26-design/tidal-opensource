-- Fixed asset register. Cost is capitalised on purchase; straight-line depreciation is posted per run.
CREATE TABLE fixed_assets (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  acquired_on date NOT NULL,
  cost_cents bigint NOT NULL CHECK (cost_cents BETWEEN 1 AND 100000000000000),
  useful_life_months integer NOT NULL CHECK (useful_life_months BETWEEN 1 AND 600),
  depreciated_months integer NOT NULL DEFAULT 0 CHECK (depreciated_months >= 0),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  CHECK (depreciated_months <= useful_life_months)
);

-- One depreciation entry per asset per month. The unique key stops a repeated run from double-depreciating.
CREATE TABLE asset_depreciation (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL,
  asset_id bigint NOT NULL,
  period_month date NOT NULL CHECK (period_month = date_trunc('month', period_month)::date),
  amount_cents bigint NOT NULL CHECK (amount_cents > 0),
  journal_id bigint NOT NULL,
  UNIQUE (org_id, asset_id, period_month),
  FOREIGN KEY (org_id, asset_id) REFERENCES fixed_assets (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE TRIGGER asset_depreciation_append_only BEFORE UPDATE OR DELETE ON asset_depreciation
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
