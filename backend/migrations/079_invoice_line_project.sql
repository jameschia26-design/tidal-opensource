-- A line may be billed to one project. Reports that group by project can then use the line, not just the header.
ALTER TABLE invoice_lines ADD COLUMN project_id bigint,
  ADD FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id);
