-- Project time entries; task names are free text until project task management exists.
CREATE TABLE time_entries (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  project_id bigint NOT NULL,
  entry_date date NOT NULL,
  task text NOT NULL CHECK (length(trim(task)) BETWEEN 1 AND 200),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  minutes integer NOT NULL CHECK (minutes BETWEEN 1 AND 1440),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id)
);
