-- Per-line discount on invoices. The line amount is the quantity times price less its own discount,
-- rounded half up to the cent, so header totals still equal the sum of line amounts.
ALTER TABLE invoice_lines ADD COLUMN discount_basis_points int NOT NULL DEFAULT 0
  CHECK (discount_basis_points BETWEEN 0 AND 10000);
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint WHERE conrelid='invoice_lines'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%quantity * unit_price_cents%';
  EXECUTE format('ALTER TABLE invoice_lines DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_amount_cents_check
  CHECK (amount_cents = ((quantity * unit_price_cents * (10000 - discount_basis_points)) + 5000) / 10000);
