-- Purchase orders can carry GST per line, like bills. Stored total = net after commercial adjustments + line GST.
-- Older orders have no gst_cents in their lines, so quote_line_gst is 0 and their totals still hold.
-- A PO is a commitment and posts nothing, so it stays out of the GST reports until it becomes a bill.
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint WHERE conrelid='purchase_orders'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%valid_quote_lines%';
  IF c IS NULL THEN RAISE EXCEPTION 'purchase_orders total constraint not found'; END IF;
  EXECUTE format('ALTER TABLE purchase_orders DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_lines_check CHECK (
  valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) + quote_line_gst(lines) = total_cents
);
