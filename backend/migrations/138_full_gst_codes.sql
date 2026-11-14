-- Full Singapore GST code set on document lines: imported services (IM), low-value goods (LVG),
-- reverse charge (RC), partially exempt purchases (PE) and bad debt relief (BD) join the standard,
-- zero, exempt, out-of-scope and no-tax codes already stored.
ALTER TABLE bill_lines DROP CONSTRAINT IF EXISTS bill_lines_gst_code_check;
ALTER TABLE bill_lines ADD CONSTRAINT bill_lines_gst_code_check
  CHECK (gst_code IN ('NONE','SR','ZR','ES','OS','IM','LVG','RC','PE','BD'));
ALTER TABLE invoice_lines DROP CONSTRAINT IF EXISTS invoice_lines_gst_code_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_gst_code_check
  CHECK (gst_code IN ('NONE','SR','ZR','ES','OS','IM','LVG','RC','PE','BD'));

-- Self-assessed lines (imported services, low-value goods, reverse charge) carry their GST on the
-- line for display and the return, but the supplier bill total excludes it. The total check must
-- therefore count only the tax the supplier actually charges.
CREATE OR REPLACE FUNCTION check_bill_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE doc bigint; total bigint; amount bigint; n int;
BEGIN
  IF TG_TABLE_NAME='bills' THEN doc:=NEW.id;
  ELSIF TG_OP='DELETE' THEN doc:=OLD.bill_id;
  ELSE doc:=NEW.bill_id; END IF;
  SELECT total_cents INTO total FROM bills WHERE id=doc;
  SELECT COALESCE(sum(l.amount_cents),0) + COALESCE(sum(CASE WHEN COALESCE(t.kind,'') IN ('import','reverse_charge') THEN 0 ELSE l.gst_cents END),0), count(*)
    INTO amount,n
    FROM bill_lines l LEFT JOIN LATERAL (
      SELECT kind FROM tax_rates WHERE code=l.gst_code AND applies_to IN ('purchase','both') ORDER BY effective_from DESC LIMIT 1
    ) t ON true
    WHERE l.bill_id=doc;
  IF n<1 OR amount<>total THEN
    RAISE EXCEPTION 'bill total does not match lines' USING ERRCODE='check_violation';
  END IF;
  RETURN NULL;
END $$;
