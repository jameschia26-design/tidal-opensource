-- Documents: files not tied to a transaction. folder_id NULL means Inbox. trashed_at set means Trash.
CREATE TABLE document_folders (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX document_folders_name_uq ON document_folders(org_id, lower(name));
CREATE TABLE documents (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  folder_id bigint,
  filename text NOT NULL CHECK (length(filename) BETWEEN 1 AND 200),
  content_type text NOT NULL,
  contents bytea NOT NULL CHECK (octet_length(contents) BETWEEN 1 AND 10485760),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  trashed_at timestamptz,
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, folder_id) REFERENCES document_folders(org_id, id)
);
CREATE INDEX documents_org_idx ON documents(org_id, id DESC);
