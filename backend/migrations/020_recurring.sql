-- Recurring invoice templates. Each run creates one invoice draft; drafts are reviewed and posted by a person.
CREATE TABLE recurring_templates (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  frequency text NOT NULL CHECK (frequency IN ('monthly','quarterly','yearly')),
  next_run date NOT NULL,
  term_days integer NOT NULL DEFAULT 30 CHECK (term_days BETWEEN 0 AND 365),
  lines jsonb NOT NULL,
  total_cents bigint NOT NULL CHECK (total_cents BETWEEN 1 AND 100000000000000),
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  terms text NOT NULL DEFAULT '' CHECK (length(terms) <= 4000),
  active boolean NOT NULL DEFAULT true,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  CHECK (valid_quote_lines(lines, total_cents))
);

-- Every generated invoice records the template and run date so one run can never create two drafts.
ALTER TABLE invoices ADD COLUMN recurring_template_id bigint;
ALTER TABLE invoices ADD COLUMN recurring_run_date date;
ALTER TABLE invoices ADD CONSTRAINT invoices_recurring_run_uq UNIQUE (org_id, recurring_template_id, recurring_run_date);
ALTER TABLE invoices ADD FOREIGN KEY (org_id, recurring_template_id) REFERENCES recurring_templates (org_id, id);
