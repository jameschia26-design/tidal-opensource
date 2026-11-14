ALTER TABLE fixed_assets
  ADD COLUMN depreciation_method text NOT NULL DEFAULT 'straight_line' CHECK (depreciation_method IN ('straight_line','diminishing_value','manual')),
  ADD COLUMN averaging_rule text NOT NULL DEFAULT 'full_month' CHECK (averaging_rule IN ('full_month','half_month')),
  ADD COLUMN residual_value_cents bigint NOT NULL DEFAULT 0 CHECK (residual_value_cents BETWEEN 0 AND cost_cents);
