-- Credit notes reduce a posted invoice. Posted credit notes are append-only; total credited per invoice cannot exceed the invoice total.
CREATE TABLE credit_notes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  invoice_id bigint NOT NULL,
  contact_id bigint NOT NULL,
  credit_date date NOT NULL,
  reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 500),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN 1 AND 100000000000000),
  journal_id bigint NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id)
);
CREATE TRIGGER credit_notes_append_only BEFORE UPDATE OR DELETE ON credit_notes
  FOR EACH ROW EXECUTE FUNCTION forbid_change();

-- A credit note can only target a posted invoice, and credits cannot exceed what was billed.
CREATE FUNCTION credit_note_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE inv_total bigint; inv_status text; already bigint;
BEGIN
  SELECT total_cents, status INTO inv_total, inv_status FROM invoices WHERE org_id=NEW.org_id AND id=NEW.invoice_id FOR UPDATE;
  IF inv_status IS DISTINCT FROM 'posted' THEN
    RAISE EXCEPTION 'credit note requires a posted invoice' USING ERRCODE = 'check_violation';
  END IF;
  SELECT COALESCE(sum(amount_cents),0) INTO already FROM credit_notes WHERE org_id=NEW.org_id AND invoice_id=NEW.invoice_id;
  IF already + NEW.amount_cents > inv_total THEN
    RAISE EXCEPTION 'credit notes exceed invoice total' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER credit_notes_guard BEFORE INSERT ON credit_notes FOR EACH ROW EXECUTE FUNCTION credit_note_guard();
