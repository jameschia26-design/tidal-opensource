-- R25/D10: keep documents for at least the agreed period. A document linked to a posted expense inside
-- retention, or under legal hold, cannot be deleted for good; moving it to the trash stays allowed.
ALTER TABLE organizations ADD COLUMN retention_years int NOT NULL DEFAULT 5 CHECK (retention_years BETWEEN 5 AND 25);
ALTER TABLE documents ADD COLUMN legal_hold boolean NOT NULL DEFAULT false;

CREATE FUNCTION document_retention_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  years int;
BEGIN
  IF OLD.legal_hold THEN
    RAISE EXCEPTION 'document retained: legal hold' USING ERRCODE = 'P0001';
  END IF;
  SELECT retention_years INTO years FROM organizations WHERE id=OLD.org_id;
  IF EXISTS (
    SELECT 1 FROM expense_receipts er JOIN expenses e ON e.org_id=er.org_id AND e.id=er.expense_id
    WHERE er.org_id=OLD.org_id AND er.document_id=OLD.id
      AND e.expense_date > current_date - make_interval(years => COALESCE(years, 5))
  ) THEN
    RAISE EXCEPTION 'document retained: linked to a posted record inside the retention period' USING ERRCODE = 'P0001';
  END IF;
  RETURN OLD;
END $$;
CREATE TRIGGER document_retention_guard BEFORE DELETE ON documents
  FOR EACH ROW EXECUTE FUNCTION document_retention_guard();
