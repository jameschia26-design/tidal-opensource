-- A journal is complete when its transaction commits: lines may only be added by the transaction that
-- created the journal, and every journal must have at least two lines. Previously a committed journal
-- could gain a balanced pair of lines and an empty journal could be committed.
CREATE FUNCTION journal_lines_same_transaction() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE created_age int;
BEGIN
  SELECT age(xmin) INTO created_age FROM journals WHERE org_id=NEW.org_id AND id=NEW.journal_id;
  IF created_age IS NULL THEN
    RAISE EXCEPTION 'journal % does not exist', NEW.journal_id USING ERRCODE = 'check_violation';
  END IF;
  IF created_age <> 0 THEN
    RAISE EXCEPTION 'journal % is sealed: lines cannot be added after it was posted', NEW.journal_id
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER journal_lines_same_transaction BEFORE INSERT ON journal_lines
  FOR EACH ROW EXECUTE FUNCTION journal_lines_same_transaction();

CREATE FUNCTION check_journal_has_lines() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE n int;
BEGIN
  SELECT count(*) INTO n FROM journal_lines WHERE org_id=NEW.org_id AND journal_id=NEW.id;
  IF n < 2 THEN
    RAISE EXCEPTION 'journal % has % lines: a journal needs at least two', NEW.id, n
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER journals_have_lines AFTER INSERT ON journals
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_journal_has_lines();
