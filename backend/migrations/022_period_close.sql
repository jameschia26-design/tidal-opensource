-- Period close: once a company closes a period, no new journal may be dated on or before the closed-through date.
-- Enforced in the database, so every write path (manual, invoice, bill, expense, credit note) is covered.
CREATE TABLE period_closes (
  org_id bigint PRIMARY KEY REFERENCES organizations(id),
  closed_through date NOT NULL,
  closed_by bigint NOT NULL REFERENCES users(id),
  closed_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION journal_period_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE closed date;
BEGIN
  SELECT closed_through INTO closed FROM period_closes WHERE org_id=NEW.org_id;
  IF closed IS NOT NULL AND NEW.entry_date <= closed THEN
    RAISE EXCEPTION 'period closed through %', closed USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER journals_period_guard BEFORE INSERT ON journals FOR EACH ROW EXECUTE FUNCTION journal_period_guard();
