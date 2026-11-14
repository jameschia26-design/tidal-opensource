ALTER TABLE bills
  ADD COLUMN order_number text NOT NULL DEFAULT '' CHECK (length(order_number) <= 100),
  ADD COLUMN subject text NOT NULL DEFAULT '' CHECK (length(subject) <= 200),
  ADD COLUMN payment_terms_days int NOT NULL DEFAULT 0 CHECK (payment_terms_days BETWEEN 0 AND 365);
