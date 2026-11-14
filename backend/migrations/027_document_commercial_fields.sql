ALTER TABLE invoices ADD COLUMN commercial jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(commercial)='object');
ALTER TABLE quotes ADD COLUMN commercial jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(commercial)='object');

-- Amounts remain integer cents; percentage discounts use basis points (100 = 1%).
CREATE FUNCTION commercial_total(subtotal bigint, details jsonb) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
 SELECT subtotal - CASE WHEN COALESCE(details->>'discount_type','flat')='percent'
   THEN div(subtotal * COALESCE((details->>'discount_basis_points')::numeric,0) + 5000,10000)
   ELSE COALESCE((details->>'discount_cents')::bigint,0) END
   + COALESCE((details->>'shipping_cents')::bigint,0) + COALESCE((details->>'adjustment_cents')::bigint,0)
$$;

CREATE OR REPLACE FUNCTION check_invoice_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE inv bigint; s bigint; n int; t bigint; details jsonb;
BEGIN
 IF TG_TABLE_NAME='invoices' THEN inv:=NEW.id;
 ELSIF TG_OP='DELETE' THEN inv:=OLD.invoice_id;
 ELSE inv:=NEW.invoice_id; END IF;
 SELECT COALESCE(sum(amount_cents),0),count(*) INTO s,n FROM invoice_lines WHERE invoice_id=inv;
 SELECT total_cents,commercial INTO t,details FROM invoices WHERE id=inv;
 IF n<1 OR commercial_total(s,details)<>t THEN
   RAISE EXCEPTION 'invoice totals do not match lines and adjustments' USING ERRCODE='check_violation';
 END IF;
 RETURN NULL;
END $$;
CREATE FUNCTION quote_line_subtotal(items jsonb) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$
 SELECT COALESCE(sum((value->>'amount_cents')::bigint),0) FROM jsonb_array_elements(items)
$$;
DO $$ DECLARE constraint_name text;
BEGIN
 SELECT conname INTO constraint_name FROM pg_constraint
 WHERE conrelid='quotes'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%valid_quote_lines%';
 EXECUTE format('ALTER TABLE quotes DROP CONSTRAINT %I',constraint_name);
END $$;
ALTER TABLE quotes ADD CONSTRAINT quote_lines_commercial_total CHECK (
 valid_quote_lines(lines,quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines),commercial)=total_cents
);

CREATE FUNCTION invoice_commercial_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='posted' AND NEW.commercial IS DISTINCT FROM OLD.commercial THEN
   RAISE EXCEPTION 'invoice posting cannot change commercial fields';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER invoice_commercial_guard BEFORE UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION invoice_commercial_guard();
