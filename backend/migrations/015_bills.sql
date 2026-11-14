CREATE TABLE bills (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  supplier_reference text NOT NULL CHECK (length(trim(supplier_reference)) BETWEEN 1 AND 100),
  bill_date date NOT NULL,
  due_date date NOT NULL CHECK (due_date >= bill_date),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','posted')),
  total_cents bigint NOT NULL CHECK (total_cents BETWEEN 1 AND 100000000000000),
  journal_id bigint,
  supplier_snapshot jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(supplier_snapshot)='object'),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  posted_at timestamptz,
  UNIQUE(org_id,id),
  UNIQUE(org_id,contact_id,supplier_reference),
  FOREIGN KEY(org_id,contact_id) REFERENCES contacts(org_id,id),
  FOREIGN KEY(org_id,journal_id) REFERENCES journals(org_id,id),
  CHECK ((status='draft' AND journal_id IS NULL AND posted_at IS NULL) OR (status='posted' AND journal_id IS NOT NULL AND posted_at IS NOT NULL))
);
CREATE TABLE bill_lines (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL,
  bill_id bigint NOT NULL,
  line_no int NOT NULL CHECK (line_no BETWEEN 1 AND 200),
  account_id bigint NOT NULL,
  description text NOT NULL CHECK (length(trim(description)) BETWEEN 1 AND 200),
  quantity bigint NOT NULL CHECK (quantity BETWEEN 1 AND 1000000),
  unit_price_cents bigint NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 100000000000000),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 0 AND 100000000000000 AND amount_cents=quantity*unit_price_cents),
  UNIQUE(bill_id,line_no),
  FOREIGN KEY(org_id,bill_id) REFERENCES bills(org_id,id),
  FOREIGN KEY(org_id,account_id) REFERENCES accounts(org_id,id)
);
CREATE TABLE bill_payments (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL,
  bill_id bigint NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  payment_date date NOT NULL,
  reference text NOT NULL DEFAULT '' CHECK (length(reference)<=100),
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 90),
  request_hash bytea NOT NULL,
  journal_id bigint NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(org_id,idempotency_key),
  FOREIGN KEY(org_id,bill_id) REFERENCES bills(org_id,id),
  FOREIGN KEY(org_id,journal_id) REFERENCES journals(org_id,id)
);
CREATE INDEX bills_company_status ON bills(org_id,status,id);
CREATE INDEX bill_payments_document ON bill_payments(org_id,bill_id);
CREATE TRIGGER bills_no_delete BEFORE DELETE ON bills FOR EACH ROW EXECUTE FUNCTION forbid_change();
CREATE TRIGGER bill_payments_append_only BEFORE UPDATE OR DELETE ON bill_payments FOR EACH ROW EXECUTE FUNCTION forbid_change();

CREATE FUNCTION bill_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status='posted' OR NEW.org_id<>OLD.org_id OR NEW.id<>OLD.id OR NEW.created_by<>OLD.created_by OR NEW.created_at<>OLD.created_at THEN
    RAISE EXCEPTION 'bill is immutable' USING ERRCODE='insufficient_privilege';
  END IF;
  IF NEW.status='posted' AND (NEW.contact_id<>OLD.contact_id OR NEW.supplier_reference<>OLD.supplier_reference OR NEW.bill_date<>OLD.bill_date OR NEW.due_date<>OLD.due_date OR NEW.notes<>OLD.notes OR NEW.total_cents<>OLD.total_cents) THEN
    RAISE EXCEPTION 'posting cannot change bill contents' USING ERRCODE='insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER bills_guard BEFORE UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION bill_guard();
CREATE FUNCTION bill_line_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE st text;
BEGIN
  IF TG_OP='UPDATE' AND (NEW.org_id<>OLD.org_id OR NEW.bill_id<>OLD.bill_id) THEN
    RAISE EXCEPTION 'bill lines cannot move' USING ERRCODE='insufficient_privilege';
  END IF;
  IF TG_OP='DELETE' THEN
    SELECT status INTO st FROM bills WHERE org_id=OLD.org_id AND id=OLD.bill_id FOR UPDATE;
  ELSE
    SELECT status INTO st FROM bills WHERE org_id=NEW.org_id AND id=NEW.bill_id FOR UPDATE;
  END IF;
  IF st IS DISTINCT FROM 'draft' THEN
    RAISE EXCEPTION 'posted bill lines are immutable' USING ERRCODE='insufficient_privilege';
  END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER bill_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON bill_lines FOR EACH ROW EXECUTE FUNCTION bill_line_guard();
CREATE FUNCTION check_bill_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE doc bigint; total bigint; amount bigint; n int;
BEGIN
  IF TG_TABLE_NAME='bills' THEN doc:=NEW.id;
  ELSIF TG_OP='DELETE' THEN doc:=OLD.bill_id;
  ELSE doc:=NEW.bill_id; END IF;
  SELECT total_cents INTO total FROM bills WHERE id=doc;
  SELECT COALESCE(sum(amount_cents),0),count(*) INTO amount,n FROM bill_lines WHERE bill_id=doc;
  IF n<1 OR amount<>total THEN
    RAISE EXCEPTION 'bill total does not match lines' USING ERRCODE='check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER bill_lines_total AFTER INSERT OR UPDATE OR DELETE ON bill_lines DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_bill_total();
CREATE CONSTRAINT TRIGGER bill_header_total AFTER INSERT OR UPDATE ON bills DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_bill_total();
CREATE FUNCTION check_bill_payment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE st text; total bigint; paid bigint; issued date;
BEGIN
  SELECT status,total_cents,bill_date INTO st,total,issued FROM bills WHERE org_id=NEW.org_id AND id=NEW.bill_id FOR UPDATE;
  SELECT COALESCE(sum(amount_cents),0) INTO paid FROM bill_payments WHERE org_id=NEW.org_id AND bill_id=NEW.bill_id;
  IF st IS DISTINCT FROM 'posted' OR paid>total OR NEW.payment_date<issued THEN
    RAISE EXCEPTION 'invalid bill payment' USING ERRCODE='check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER bill_payment_total AFTER INSERT ON bill_payments DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_bill_payment();
