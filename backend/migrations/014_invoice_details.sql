ALTER TABLE invoices
  ADD COLUMN reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  ADD COLUMN notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  ADD COLUMN terms text NOT NULL DEFAULT '' CHECK (length(terms) <= 4000),
  ADD COLUMN seller_snapshot jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(seller_snapshot) = 'object'),
  ADD COLUMN customer_snapshot jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(customer_snapshot) = 'object');

CREATE OR REPLACE FUNCTION invoice_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'posted' THEN
    RAISE EXCEPTION 'posted invoice % is immutable', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.org_id <> OLD.org_id OR NEW.id <> OLD.id OR NEW.created_by <> OLD.created_by
     OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'invoice identity cannot change' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status = 'draft' AND NEW.number IS NULL AND NEW.journal_id IS NULL AND NEW.posted_at IS NULL THEN
    RETURN NEW;
  END IF;
  IF NEW.status <> 'posted' OR NEW.number IS NULL OR NEW.journal_id IS NULL OR NEW.posted_at IS NULL
     OR NEW.contact_id <> OLD.contact_id OR NEW.total_cents <> OLD.total_cents
     OR NEW.invoice_date <> OLD.invoice_date OR NEW.due_date <> OLD.due_date
     OR NEW.reference <> OLD.reference OR NEW.notes <> OLD.notes OR NEW.terms <> OLD.terms THEN
    RAISE EXCEPTION 'invalid invoice posting' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER invoice_lines_append_only ON invoice_lines;
CREATE FUNCTION invoice_line_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  st text;
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.org_id <> OLD.org_id OR NEW.invoice_id <> OLD.invoice_id) THEN
    RAISE EXCEPTION 'invoice lines cannot move between invoices' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF TG_OP <> 'INSERT' THEN
    SELECT status INTO st FROM invoices WHERE org_id=OLD.org_id AND id=OLD.invoice_id FOR UPDATE;
    IF st IS DISTINCT FROM 'draft' THEN
      RAISE EXCEPTION 'posted invoice lines are immutable' USING ERRCODE = 'insufficient_privilege';
    END IF;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    SELECT status INTO st FROM invoices WHERE org_id=NEW.org_id AND id=NEW.invoice_id FOR UPDATE;
    IF st IS DISTINCT FROM 'draft' THEN
      RAISE EXCEPTION 'posted invoice lines are immutable' USING ERRCODE = 'insufficient_privilege';
    END IF;
    RETURN NEW;
  END IF;
  RETURN OLD;
END $$;
CREATE TRIGGER invoice_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON invoice_lines
  FOR EACH ROW EXECUTE FUNCTION invoice_line_guard();

-- Recheck totals for changes to either header or lines, including deletion of every line.
CREATE OR REPLACE FUNCTION check_invoice_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  inv bigint;
  s bigint;
  n int;
  t bigint;
BEGIN
  IF TG_TABLE_NAME = 'invoices' THEN
    inv := NEW.id;
  ELSIF TG_OP = 'DELETE' THEN
    inv := OLD.invoice_id;
  ELSE
    inv := NEW.invoice_id;
  END IF;
  SELECT COALESCE(SUM(amount_cents),0),COUNT(*) INTO s,n FROM invoice_lines WHERE invoice_id=inv;
  SELECT total_cents INTO t FROM invoices WHERE id=inv;
  IF n < 1 OR s <> t THEN
    RAISE EXCEPTION 'invoice totals do not match lines' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
DROP TRIGGER invoice_lines_total ON invoice_lines;
CREATE CONSTRAINT TRIGGER invoice_lines_total AFTER INSERT OR UPDATE OR DELETE ON invoice_lines
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_invoice_total();
CREATE CONSTRAINT TRIGGER invoice_header_total AFTER INSERT OR UPDATE ON invoices
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_invoice_total();
