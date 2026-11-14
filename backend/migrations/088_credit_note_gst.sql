-- Customer credit notes can carry GST, like invoices. Each stored line gains gst_code, gst_rate_bp
-- and gst_cents; the document total becomes net lines plus commercial adjustment plus line GST.
CREATE FUNCTION quote_line_gst(items jsonb) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
  SELECT COALESCE(sum(COALESCE(gst_cents,0)),0) FROM jsonb_to_recordset(items) AS l(gst_cents bigint)
$$;
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint WHERE conrelid='customer_credit_notes'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%valid_quote_lines%';
  IF c IS NOT NULL THEN EXECUTE format('ALTER TABLE customer_credit_notes DROP CONSTRAINT %I', c); END IF;
END $$;
ALTER TABLE customer_credit_notes ADD CONSTRAINT customer_credit_notes_lines_check
  CHECK (valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) + quote_line_gst(lines) = total_cents);
