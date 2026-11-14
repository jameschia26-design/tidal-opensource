-- A rule can narrow itself with an amount range and a reference match, and rules run in priority order.
-- All conditions are optional except the description text; NULL means "no condition".
ALTER TABLE bank_rules
  ADD COLUMN amount_min_cents bigint CHECK (amount_min_cents IS NULL OR amount_min_cents >= 0),
  ADD COLUMN amount_max_cents bigint CHECK (amount_max_cents IS NULL OR amount_max_cents >= 0),
  ADD COLUMN reference_contains text CHECK (reference_contains IS NULL OR length(trim(reference_contains)) BETWEEN 1 AND 100),
  ADD COLUMN priority int NOT NULL DEFAULT 100 CHECK (priority BETWEEN 1 AND 999);
ALTER TABLE bank_rules ADD CHECK (amount_min_cents IS NULL OR amount_max_cents IS NULL OR amount_min_cents <= amount_max_cents);
