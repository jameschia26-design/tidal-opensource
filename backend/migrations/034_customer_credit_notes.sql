-- Standalone customer credit notes (not tied to one invoice). Draft is editable; open posts Dr revenue, Cr receivable and is then frozen.
CREATE TABLE customer_credit_notes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  credit_number text NOT NULL CHECK (length(trim(credit_number)) BETWEEN 1 AND 50),
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  credit_date date NOT NULL,
  lines jsonb NOT NULL,
  commercial jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(commercial)='object'),
  total_cents bigint NOT NULL CHECK (total_cents BETWEEN 1 AND 100000000000000),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  terms text NOT NULL DEFAULT '' CHECK (length(terms) <= 4000),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','open','void')),
  journal_id bigint,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  posted_at timestamptz,
  UNIQUE (org_id, id),
  UNIQUE (org_id, credit_number),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id),
  CHECK (valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) = total_cents),
  CHECK ((status='open' AND journal_id IS NOT NULL AND posted_at IS NOT NULL) OR (status<>'open' AND journal_id IS NULL AND posted_at IS NULL))
);
CREATE FUNCTION customer_credit_note_freeze() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR OLD.status<>'draft' THEN
    RAISE EXCEPTION 'only draft credit notes can change' USING ERRCODE='check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER customer_credit_notes_freeze BEFORE UPDATE OR DELETE ON customer_credit_notes FOR EACH ROW EXECUTE FUNCTION customer_credit_note_freeze();
