-- Recurring bill profiles. Each run creates one DRAFT bill per due date; a person reviews and posts it.
CREATE TABLE recurring_bill_profiles (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  repeat_every integer NOT NULL CHECK (repeat_every BETWEEN 1 AND 12),
  repeat_unit text NOT NULL CHECK (repeat_unit IN ('week','month','year')),
  start_date date NOT NULL,
  ends_on date CHECK (ends_on IS NULL OR ends_on >= start_date),
  next_run date NOT NULL,
  runs_done integer NOT NULL DEFAULT 0,
  active boolean NOT NULL DEFAULT true,
  data jsonb NOT NULL CHECK (jsonb_typeof(data)='object'),
  term_days integer NOT NULL DEFAULT 30 CHECK (term_days BETWEEN 0 AND 365),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
ALTER TABLE bills ADD COLUMN recurring_profile_id bigint, ADD COLUMN recurring_run_date date;
ALTER TABLE bills ADD FOREIGN KEY (org_id, recurring_profile_id) REFERENCES recurring_bill_profiles (org_id, id);
ALTER TABLE bills ADD CONSTRAINT bills_recurring_run_uq UNIQUE (org_id, recurring_profile_id, recurring_run_date);
