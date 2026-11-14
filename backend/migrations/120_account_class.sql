-- Xero-level account classes under the broad account type.
-- The broad type (asset/liability/equity/revenue/expense) stays the posting and reporting group, so
-- existing report maths is unchanged; class is what the chart, P&L shape and balance sheet split on.
ALTER TABLE accounts ADD COLUMN class text;

-- Backfill every existing account from the data already on the row. Rules are per broad type:
--   revenue    -> sales when the code starts 4 (the sales range), otherwise revenue
--   expense    -> cost_of_sales when the code starts 5 (the direct-cost range), otherwise expense
--   asset      -> bank for codes starting 28, fixed_asset for 15/17, inventory for 24,
--                 anything else current_asset
--   liability  -> current_liability (the chart does not record a non-current split)
--   equity     -> retained_earnings for a name that records retained/accumulated profit,
--                 otherwise equity
-- Codes are the Tidal chart convention (1000 Bank, 1500 Fixed assets, 2400 Inventory, 5100 Cost of
-- Sales, 4000 Sales) so the prefix is the reliable signal; the name check is only for equity, where
-- no code range marks retained earnings.
UPDATE accounts SET class = CASE type
  WHEN 'revenue' THEN CASE WHEN code LIKE '4%' THEN 'sales' ELSE 'revenue' END
  WHEN 'expense' THEN CASE WHEN code LIKE '5%' THEN 'cost_of_sales' ELSE 'expense' END
  WHEN 'asset' THEN CASE
    WHEN code LIKE '28%' THEN 'bank'
    WHEN code LIKE '15%' OR code LIKE '17%' THEN 'fixed_asset'
    WHEN code LIKE '24%' THEN 'inventory'
    ELSE 'current_asset' END
  WHEN 'liability' THEN 'current_liability'
  ELSE CASE WHEN name ILIKE '%retained%' OR name ILIKE '%accumulated%profit%' THEN 'retained_earnings' ELSE 'equity' END
END;

ALTER TABLE accounts ALTER COLUMN class SET NOT NULL;

ALTER TABLE accounts ADD CONSTRAINT accounts_class_check CHECK (class IN (
  'bank', 'current_asset', 'fixed_asset', 'inventory', 'prepayment', 'non_current_asset',
  'current_liability', 'non_current_liability', 'equity', 'retained_earnings',
  'sales', 'revenue', 'other_income', 'cost_of_sales', 'expense', 'other_expense'));

-- The two columns must agree: a class always sits inside its broad type group, so no write can put a
-- revenue class on an asset account and quietly mis-group a report.
ALTER TABLE accounts ADD CONSTRAINT accounts_class_group_check CHECK (
  (type = 'asset' AND class IN ('bank', 'current_asset', 'fixed_asset', 'inventory', 'prepayment', 'non_current_asset'))
  OR (type = 'liability' AND class IN ('current_liability', 'non_current_liability'))
  OR (type = 'equity' AND class IN ('equity', 'retained_earnings'))
  OR (type = 'revenue' AND class IN ('sales', 'revenue', 'other_income'))
  OR (type = 'expense' AND class IN ('cost_of_sales', 'expense', 'other_expense')));
