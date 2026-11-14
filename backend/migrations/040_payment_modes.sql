-- Custom payment modes. Documents keep storing the mode as text: a built-in key or a company-defined name.
-- The fixed CHECK lists are replaced by a length check; the handlers validate against built-ins plus this table.
CREATE TABLE payment_modes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 50),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX payment_modes_name ON payment_modes (org_id, lower(name));

DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.conrelid::regclass AS tbl, c.conname FROM pg_constraint c
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
    WHERE c.contype = 'c' AND a.attname = 'payment_mode' AND c.conrelid::regclass::text IN ('sales_receipts','payments_received','payments_made')
  LOOP
    EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', r.tbl, r.conname);
  END LOOP;
END $$;
ALTER TABLE sales_receipts ADD CONSTRAINT sales_receipts_payment_mode_len CHECK (length(payment_mode) BETWEEN 1 AND 50);
ALTER TABLE payments_received ADD CONSTRAINT payments_received_payment_mode_len CHECK (length(payment_mode) BETWEEN 1 AND 50);
ALTER TABLE payments_made ADD CONSTRAINT payments_made_payment_mode_len CHECK (length(payment_mode) BETWEEN 1 AND 50);
