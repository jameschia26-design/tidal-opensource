ALTER TABLE leads
  ADD COLUMN company_name text NOT NULL DEFAULT '' CHECK (length(company_name) <= 200),
  ADD COLUMN phone text NOT NULL DEFAULT '' CHECK (length(phone) <= 40),
  ADD COLUMN job_title text NOT NULL DEFAULT '' CHECK (length(job_title) <= 100),
  ADD COLUMN website text NOT NULL DEFAULT '' CHECK (length(website) <= 300),
  ADD COLUMN notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  ADD COLUMN contact_id bigint,
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  ADD FOREIGN KEY (org_id,contact_id) REFERENCES contacts(org_id,id);

CREATE TABLE activities (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  lead_id bigint NOT NULL,
  kind text NOT NULL CHECK (kind IN ('task','call','meeting','email','note')),
  subject text NOT NULL CHECK (length(trim(subject)) BETWEEN 1 AND 200),
  due_date date,
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  completed_at timestamptz,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id,lead_id) REFERENCES leads(org_id,id)
);
CREATE INDEX activities_company_due ON activities(org_id,due_date,id);
