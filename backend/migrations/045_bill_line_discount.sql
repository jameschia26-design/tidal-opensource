-- Bill line discount and per-line customer. amount_cents becomes the net amount after discount.
ALTER TABLE bill_lines
  ADD COLUMN discount_cents bigint NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
  ADD COLUMN discount_basis_points int NOT NULL DEFAULT 0 CHECK (discount_basis_points BETWEEN 0 AND 10000),
  ADD COLUMN customer_id bigint,
  ADD FOREIGN KEY (org_id, customer_id) REFERENCES contacts(org_id, id);
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint WHERE conrelid='bill_lines'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%quantity * unit_price_cents%';
  EXECUTE format('ALTER TABLE bill_lines DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE bill_lines ADD CONSTRAINT bill_lines_net_amount_check
  CHECK (amount_cents BETWEEN 0 AND 100000000000000 AND amount_cents + discount_cents = quantity * unit_price_cents);
