-- Tax model (Phase 1c): dated rate catalogue, per-account default rate, organisation tax settings and
-- one GST control liability.
--
-- The catalogue is statutory data, not tenant configuration: the same rows serve every company, and a
-- document resolves its code to a rate by its own date. Existing document codes (NONE, SR, ZR, ES, OS)
-- stay valid and now resolve through this table.

CREATE TABLE tax_rates (
  id             bigserial PRIMARY KEY,
  code           text NOT NULL CHECK (code ~ '^[A-Z0-9_]{1,20}$'),
  name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  rate_bp        bigint NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
  kind           text NOT NULL CHECK (kind IN ('standard', 'zero', 'exempt', 'out_of_scope', 'reverse_charge', 'import', 'no_tax')),
  applies_to     text NOT NULL CHECK (applies_to IN ('sale', 'purchase', 'both')),
  effective_from date NOT NULL,
  effective_to   date,
  is_active      boolean NOT NULL DEFAULT true,
  CHECK (effective_to IS NULL OR effective_to >= effective_from)
);
CREATE UNIQUE INDEX tax_rates_period_uq ON tax_rates (code, applies_to, effective_from);
CREATE INDEX tax_rates_lookup_idx ON tax_rates (code, effective_from);

-- Standard rate history mirrors gst_rates (the statutory source kept by migration 096), so a code and
-- a document date always give the same answer whichever table is read.
INSERT INTO tax_rates (code, name, rate_bp, kind, applies_to, effective_from, effective_to) VALUES
  ('SR', 'GST on Income 3%', 300, 'standard', 'both', '1994-04-01', '2002-12-31'),
  ('SR', 'GST on Income 4%', 400, 'standard', 'both', '2003-01-01', '2003-12-31'),
  ('SR', 'GST on Income 5%', 500, 'standard', 'both', '2004-01-01', '2007-06-30'),
  ('SR', 'GST on Income 7%', 700, 'standard', 'both', '2007-07-01', '2022-12-31'),
  ('SR', 'GST on Income 8%', 800, 'standard', 'both', '2023-01-01', '2023-12-31'),
  ('SR', 'GST on Income 9%', 900, 'standard', 'both', '2024-01-01', NULL),
  ('ZR', 'Zero-rated', 0, 'zero', 'both', '1994-04-01', NULL),
  ('ES', 'Exempt', 0, 'exempt', 'both', '1994-04-01', NULL),
  ('OS', 'Out of scope', 0, 'out_of_scope', 'both', '1994-04-01', NULL),
  ('NONE', 'No tax', 0, 'no_tax', 'both', '1994-04-01', NULL),
  ('IM', 'Imported services (IGDS)', 900, 'import', 'purchase', '1994-04-01', NULL),
  ('LVG', 'Low-value goods (LVG)', 900, 'import', 'purchase', '2023-01-01', NULL),
  ('RC', 'Reverse charge', 900, 'reverse_charge', 'purchase', '1994-04-01', NULL),
  ('PE', 'Partially exempt purchases', 0, 'exempt', 'purchase', '1994-04-01', NULL),
  ('BD', 'Bad debt relief', 0, 'standard', 'purchase', '1994-04-01', NULL);

-- A purchase's standard rate follows the same history; the current code SR already covers it.

-- Default tax rate per account: the rate a new line on that account starts with. A line may still
-- choose another code. Blank keeps today's behaviour (no tax unless the line says otherwise).
ALTER TABLE accounts ADD COLUMN tax_rate_code text CHECK (tax_rate_code ~ '^[A-Z0-9_]{1,20}$');

-- Organisation tax settings, as Xero's Financial settings screen holds them.
ALTER TABLE organizations
  ADD COLUMN tax_basis text NOT NULL DEFAULT 'accrual' CHECK (tax_basis IN ('accrual', 'payment')),
  ADD COLUMN tax_display_name text NOT NULL DEFAULT 'GST' CHECK (length(tax_display_name) <= 40),
  ADD COLUMN tax_period text NOT NULL DEFAULT 'quarterly' CHECK (tax_period IN ('monthly', 'quarterly', 'twice_monthly')),
  ADD COLUMN default_sales_treatment text NOT NULL DEFAULT 'exclusive' CHECK (default_sales_treatment IN ('exclusive', 'inclusive')),
  ADD COLUMN default_purchase_treatment text NOT NULL DEFAULT 'exclusive' CHECK (default_purchase_treatment IN ('exclusive', 'inclusive'));

-- One GST liability holds both output and input tax, so the balance sheet shows Xero's single GST line.
-- Existing companies keep account 2310 (GST Input, an asset) with its history; its balance moves into
-- 2300 through one dated journal instead of editing posted history.
UPDATE accounts SET name = 'GST' WHERE code = '2300' AND name = 'GST Output';

-- tax_direction records which box a GST journal line belongs to now that both live on one account.
-- NULL means "derive from the account code" (2300 output, 2310 input), which keeps every older
-- journal and any manual GST journal reading as before. 'none' keeps a journal out of GST returns.
ALTER TABLE journal_lines ADD COLUMN tax_direction text CHECK (tax_direction IN ('output', 'input', 'none'));

CREATE FUNCTION reclassify_gst_control(p_org bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  control bigint;
  input_gst bigint;
  balance bigint;
  author bigint;
  entry bigint;
BEGIN
  SELECT id INTO control FROM accounts WHERE org_id = p_org AND code = '2300';
  SELECT id INTO input_gst FROM accounts WHERE org_id = p_org AND code = '2310';
  IF control IS NULL OR input_gst IS NULL THEN
    RETURN; -- a company created after this migration never has the old input account
  END IF;
  SELECT COALESCE(SUM(debit_cents - credit_cents), 0) INTO balance FROM journal_lines WHERE org_id = p_org AND account_id = input_gst;
  IF balance = 0 THEN
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM journals WHERE org_id = p_org AND idempotency_key = 'gst-control-reclass:2310') THEN
    RETURN;
  END IF;
  SELECT created_by INTO author FROM journals WHERE org_id = p_org ORDER BY id LIMIT 1;
  IF author IS NULL THEN
    RETURN; -- no posted journal means no balance to move
  END IF;
  INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
    VALUES (p_org, CURRENT_DATE, 'GST control reclassification: move GST Input into the single GST account', 'gst-control-reclass:2310', '\x'::bytea, author)
    RETURNING id INTO entry;
  IF balance > 0 THEN
    INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, tax_direction, description) VALUES
      (p_org, entry, 1, control, balance, 0, 'none', 'GST control reclassification'),
      (p_org, entry, 2, input_gst, 0, balance, 'none', 'GST control reclassification');
  ELSE
    INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, tax_direction, description) VALUES
      (p_org, entry, 1, control, 0, -balance, 'none', 'GST control reclassification'),
      (p_org, entry, 2, input_gst, -balance, 0, 'none', 'GST control reclassification');
  END IF;
END $$;

SELECT reclassify_gst_control(id) FROM organizations;
