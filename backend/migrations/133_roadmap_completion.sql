-- Roadmap completion: server-side saved reports (including blank reports), project task catalogue and
-- per-person project roles, fixed-asset revaluation with rollback, and stock backorders.
CREATE TABLE saved_reports (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  title text NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 100),
  report_key text NOT NULL CHECK (length(trim(report_key)) BETWEEN 1 AND 60),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'archived')),
  params jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(params) = 'object'),
  -- Blank reports keep their own column and row layout; other reports leave this empty.
  layout jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(layout) = 'object'),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE INDEX saved_reports_org_key ON saved_reports (org_id, report_key, status);

CREATE TABLE project_tasks (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  project_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  rate_cents bigint NOT NULL DEFAULT 0 CHECK (rate_cents >= 0),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, project_id, name),
  FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id)
);

-- Project roles are per person: a lead may manage the project without being a company admin.
CREATE TABLE project_members (
  org_id bigint NOT NULL REFERENCES organizations(id),
  project_id bigint NOT NULL,
  user_id bigint NOT NULL REFERENCES users(id),
  role text NOT NULL DEFAULT 'member' CHECK (role IN ('lead', 'member')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, project_id, user_id),
  FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id)
);

-- Revaluations are dated movements on an asset. A rollback reverses the journal through the normal
-- reversal table instead of editing history, so an auditor can see both entries.
CREATE TABLE asset_revaluations (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  asset_id bigint NOT NULL,
  revalued_on date NOT NULL,
  amount_cents bigint NOT NULL CHECK (amount_cents <> 0),
  journal_id bigint NOT NULL,
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, asset_id) REFERENCES fixed_assets(org_id, id),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals(org_id, id)
);

-- A backorder is the part of an invoice line the stock could not cover. Fulfilment happens when a
-- later bill brings the item in, first in first out, and each step is append-only.
-- Rollbacks of revaluations are reversals in the normal reversal log.
ALTER TABLE document_reversals DROP CONSTRAINT document_reversals_doc_kind_check;
ALTER TABLE document_reversals ADD CONSTRAINT document_reversals_doc_kind_check
  CHECK (doc_kind IN ('bill', 'bill_payment', 'expense', 'invoice', 'customer_credit_note', 'journal', 'asset_revaluation'));

CREATE TABLE backorders (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  item_id bigint NOT NULL,
  invoice_id bigint NOT NULL,
  contact_id bigint NOT NULL,
  ordered_on date NOT NULL,
  qty bigint NOT NULL CHECK (qty > 0),
  fulfilled_qty bigint NOT NULL DEFAULT 0 CHECK (fulfilled_qty >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (fulfilled_qty <= qty),
  UNIQUE (org_id, id),
  UNIQUE (org_id, item_id, invoice_id),
  FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id),
  FOREIGN KEY (org_id, invoice_id) REFERENCES invoices(org_id, id)
);
CREATE INDEX backorders_open ON backorders (org_id, item_id) WHERE fulfilled_qty < qty;

CREATE TABLE backorder_fulfilments (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  backorder_id bigint NOT NULL,
  bill_id bigint,
  fulfilled_on date NOT NULL,
  qty bigint NOT NULL CHECK (qty > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, backorder_id) REFERENCES backorders(org_id, id)
);
CREATE TRIGGER backorder_fulfilments_append_only BEFORE UPDATE OR DELETE ON backorder_fulfilments
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
