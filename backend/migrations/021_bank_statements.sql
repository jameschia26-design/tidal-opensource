-- Bank statement lines imported from a file. Imports are append-only; a line can be matched to at most one payment.
CREATE TABLE bank_statement_lines (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  line_date date NOT NULL,
  description text NOT NULL CHECK (length(description) BETWEEN 1 AND 300),
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  amount_cents bigint NOT NULL CHECK (amount_cents BETWEEN -100000000000000 AND 100000000000000 AND amount_cents <> 0),
  import_key text NOT NULL CHECK (length(import_key) BETWEEN 1 AND 200),
  matched_payment_kind text CHECK (matched_payment_kind IN ('invoice','bill')),
  matched_payment_id bigint,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, import_key),
  UNIQUE (org_id, id),
  CHECK ((matched_payment_kind IS NULL) = (matched_payment_id IS NULL))
);
CREATE TRIGGER bank_lines_append_only BEFORE DELETE ON bank_statement_lines
  FOR EACH ROW EXECUTE FUNCTION forbid_change();

-- Match is recorded once and cannot be moved to another payment.
CREATE FUNCTION bank_line_match_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.matched_payment_id IS NOT NULL AND (NEW.matched_payment_id IS DISTINCT FROM OLD.matched_payment_id OR NEW.matched_payment_kind IS DISTINCT FROM OLD.matched_payment_kind) THEN
    RAISE EXCEPTION 'bank line match is final';
  END IF;
  IF NEW.line_date IS DISTINCT FROM OLD.line_date OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents OR NEW.description IS DISTINCT FROM OLD.description THEN
    RAISE EXCEPTION 'imported bank lines are immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER bank_line_match_guard BEFORE UPDATE ON bank_statement_lines
  FOR EACH ROW EXECUTE FUNCTION bank_line_match_guard();
