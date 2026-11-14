-- Receipts Inbox: a receipt file waits here until it is linked to an expense. Expenses stay append-only; the link is its own row.
CREATE TABLE expense_receipts (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  document_id bigint NOT NULL,
  expense_id bigint,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, document_id),
  UNIQUE (org_id, expense_id),
  FOREIGN KEY (org_id, document_id) REFERENCES documents(org_id, id),
  FOREIGN KEY (org_id, expense_id) REFERENCES expenses(org_id, id)
);
