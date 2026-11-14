-- Per-contact sales and purchase defaults, matching Xero's contact financial details.
ALTER TABLE contacts
  ADD COLUMN sales_treatment text NOT NULL DEFAULT '' CHECK (sales_treatment IN ('','exclusive','inclusive')),
  ADD COLUMN sales_tax_code text NOT NULL DEFAULT '',
  ADD COLUMN purchase_treatment text NOT NULL DEFAULT '' CHECK (purchase_treatment IN ('','exclusive','inclusive')),
  ADD COLUMN purchase_tax_code text NOT NULL DEFAULT '';
