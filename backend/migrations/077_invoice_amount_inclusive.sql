-- Tax-inclusive invoices store the GST-backed-out net, which sits below the discount-only amount,
-- so the exact equality check has to become a range: never negative, never above the discounted typed price.
ALTER TABLE invoice_lines DROP CONSTRAINT invoice_lines_amount_cents_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_amount_cents_check
  CHECK (amount_cents >= 0 AND amount_cents <= ((quantity * unit_price_cents * (10000 - discount_basis_points)) + 5000) / 10000);
