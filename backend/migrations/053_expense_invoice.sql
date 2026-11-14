CREATE TABLE expense_invoice_links (
  org_id bigint NOT NULL REFERENCES organizations(id),
  expense_id bigint NOT NULL,
  invoice_id bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, expense_id),
  UNIQUE (org_id, invoice_id),
  FOREIGN KEY (org_id, expense_id) REFERENCES expenses(org_id, id),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices(org_id, id)
);
