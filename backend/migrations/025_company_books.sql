-- Each existing organization remains its own company's first set of books.
-- Additional books reuse the same organization-scoped ledger and document boundaries.
ALTER TABLE organizations ADD COLUMN parent_company_id bigint REFERENCES organizations(id);
ALTER TABLE organizations ADD CONSTRAINT books_not_own_parent CHECK (parent_company_id IS NULL OR parent_company_id <> id);
CREATE INDEX organizations_parent_company_idx ON organizations(parent_company_id);

CREATE FUNCTION check_book_parent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_company_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM organizations WHERE id=NEW.parent_company_id
      AND parent_company_id IS NULL AND owner_user_id=NEW.owner_user_id
  ) THEN
    RAISE EXCEPTION 'Books must belong directly to their owner company';
  END IF;
  IF TG_OP='UPDATE' AND NEW.parent_company_id IS DISTINCT FROM OLD.parent_company_id THEN
    RAISE EXCEPTION 'Books cannot be moved between companies';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER book_parent_guard BEFORE INSERT OR UPDATE ON organizations
FOR EACH ROW EXECUTE FUNCTION check_book_parent();
