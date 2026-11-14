-- GST on invoices. amount_cents stays the net (ex-GST) line amount; GST is stored per line and summed on the header.
ALTER TABLE invoice_lines
  ADD COLUMN gst_code text NOT NULL DEFAULT 'NONE' CHECK (gst_code IN ('NONE', 'SR')),
  ADD COLUMN gst_rate_bp bigint NOT NULL DEFAULT 0 CHECK (gst_rate_bp >= 0),
  ADD COLUMN gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0);
ALTER TABLE invoices ADD COLUMN gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0);

-- Total = net after commercial adjustments + GST. Header GST must equal the sum of line GST.
CREATE OR REPLACE FUNCTION check_invoice_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE inv bigint; s bigint; g bigint; n int; t bigint; details jsonb; hg bigint;
BEGIN
 IF TG_TABLE_NAME='invoices' THEN inv:=NEW.id;
 ELSIF TG_OP='DELETE' THEN inv:=OLD.invoice_id;
 ELSE inv:=NEW.invoice_id; END IF;
 SELECT COALESCE(sum(amount_cents),0),COALESCE(sum(gst_cents),0),count(*) INTO s,g,n FROM invoice_lines WHERE invoice_id=inv;
 SELECT total_cents,commercial,gst_cents INTO t,details,hg FROM invoices WHERE id=inv;
 IF n<1 OR commercial_total(s,details)+g<>t OR hg<>g THEN
   RAISE EXCEPTION 'invoice totals do not match lines and adjustments' USING ERRCODE='check_violation';
 END IF;
 RETURN NULL;
END $$;
