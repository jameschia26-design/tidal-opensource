-- Sales receipts can carry GST per line, like invoices. Stored total = net after commercial adjustments + line GST.
-- Older receipts have no gst_cents in their lines, so quote_line_gst is 0 and their totals still hold.
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint WHERE conrelid='sales_receipts'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%valid_quote_lines%';
  IF c IS NULL THEN RAISE EXCEPTION 'sales_receipts total constraint not found'; END IF;
  EXECUTE format('ALTER TABLE sales_receipts DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE sales_receipts ADD CONSTRAINT sales_receipts_lines_check CHECK (
  valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) + quote_line_gst(lines) = total_cents
);
