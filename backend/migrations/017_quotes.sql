CREATE FUNCTION valid_quote_lines(items jsonb, total bigint) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
  SELECT jsonb_typeof(items)='array' AND jsonb_array_length(items) BETWEEN 1 AND 200
    AND count(*)=jsonb_array_length(items)
    AND COALESCE(bool_and(line_no IS NOT NULL AND description IS NOT NULL AND quantity IS NOT NULL AND unit_price_cents IS NOT NULL AND amount_cents IS NOT NULL
      AND line_no BETWEEN 1 AND 200 AND length(trim(description)) BETWEEN 1 AND 200
      AND quantity BETWEEN 1 AND 1000000 AND unit_price_cents BETWEEN 0 AND 100000000000000
      AND amount_cents=quantity::numeric*unit_price_cents),false)
    AND count(DISTINCT line_no)=count(*) AND sum(amount_cents)=total
  FROM jsonb_to_recordset(items) AS l(line_no integer,description text,quantity bigint,unit_price_cents bigint,amount_cents bigint)
$$;

CREATE TABLE quotes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  quote_date date NOT NULL,
  expiry_date date NOT NULL CHECK(expiry_date>=quote_date),
  reference text NOT NULL DEFAULT '' CHECK(length(reference)<=100),
  notes text NOT NULL DEFAULT '' CHECK(length(notes)<=4000),
  terms text NOT NULL DEFAULT '' CHECK(length(terms)<=4000),
  lines jsonb NOT NULL,
  total_cents bigint NOT NULL CHECK(total_cents BETWEEN 1 AND 100000000000000),
  status text NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','sent','accepted','rejected')),
  seller_snapshot jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(seller_snapshot)='object'),
  customer_snapshot jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(customer_snapshot)='object'),
  invoice_id bigint,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(org_id,id),
  UNIQUE(org_id,invoice_id),
  FOREIGN KEY(org_id,contact_id) REFERENCES contacts(org_id,id),
  FOREIGN KEY(org_id,invoice_id) REFERENCES invoices(org_id,id),
  CHECK(valid_quote_lines(lines,total_cents)),
  CHECK(invoice_id IS NULL OR status='accepted')
);

CREATE FUNCTION quote_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.org_id<>OLD.org_id OR NEW.id<>OLD.id OR NEW.created_by<>OLD.created_by OR NEW.created_at<>OLD.created_at THEN
    RAISE EXCEPTION 'quote identity is immutable';
  END IF;
  IF OLD.status<>'draft' AND
    (to_jsonb(NEW)-ARRAY['status','invoice_id','updated_at']) IS DISTINCT FROM
    (to_jsonb(OLD)-ARRAY['status','invoice_id','updated_at']) THEN
    RAISE EXCEPTION 'sent quote content is immutable';
  END IF;
  IF NEW.status<>OLD.status AND NOT ((OLD.status='draft' AND NEW.status='sent') OR (OLD.status='sent' AND NEW.status IN ('accepted','rejected'))) THEN
    RAISE EXCEPTION 'invalid quote transition';
  END IF;
  IF OLD.invoice_id IS NOT NULL AND NEW.invoice_id IS DISTINCT FROM OLD.invoice_id THEN
    RAISE EXCEPTION 'converted quote invoice is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER quotes_guard BEFORE UPDATE ON quotes FOR EACH ROW EXECUTE FUNCTION quote_guard();
CREATE TRIGGER quotes_no_delete BEFORE DELETE ON quotes FOR EACH ROW EXECUTE FUNCTION forbid_change();
