-- Projects group a customer's quotes, invoices and expenses. Documents store the project name; expenses link by id.
CREATE TABLE projects (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL,
  contact_id bigint NOT NULL,
  name       text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  active     boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts(org_id, id)
);
CREATE UNIQUE INDEX projects_name ON projects(org_id, contact_id, lower(name));
ALTER TABLE expenses ADD COLUMN project_id bigint, ADD COLUMN billable boolean NOT NULL DEFAULT false,
  ADD FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id);
