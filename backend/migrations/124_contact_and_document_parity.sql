-- Phase 2a: contact groups, smart lists, archived contacts, contact sales defaults,
-- per-invoice branding theme and tax-inclusive customer credit notes.
--
-- Archived contacts stay in the books (documents still point at them) and are hidden from
-- pickers unless a screen asks for the archived list. Nothing is deleted.

ALTER TABLE contacts
  ADD COLUMN archived boolean NOT NULL DEFAULT false,
  ADD COLUMN discount_basis_points int NOT NULL DEFAULT 0 CHECK (discount_basis_points BETWEEN 0 AND 10000),
  -- NULL means "same as the invoice terms"; a number sets the bill side separately.
  ADD COLUMN bill_payment_terms_days int CHECK (bill_payment_terms_days BETWEEN 0 AND 366);

CREATE INDEX contacts_archived_idx ON contacts (org_id, archived);

CREATE TABLE contact_groups (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL REFERENCES organizations(id),
  name       text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX contact_groups_name_uq ON contact_groups (org_id, lower(name));

CREATE TABLE contact_group_members (
  org_id     bigint NOT NULL,
  group_id   bigint NOT NULL,
  contact_id bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, contact_id),
  FOREIGN KEY (org_id, group_id) REFERENCES contact_groups (org_id, id) ON DELETE CASCADE,
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id)
);

CREATE TABLE contact_smart_lists (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL REFERENCES organizations(id),
  name       text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  query      text NOT NULL DEFAULT '' CHECK (length(query) <= 200),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX contact_smart_lists_name_uq ON contact_smart_lists (org_id, lower(name));

-- A branding theme chosen on the invoice wins over the company default when the PDF is printed.
ALTER TABLE invoices ADD COLUMN branding_theme_id bigint;
ALTER TABLE invoices ADD CONSTRAINT invoices_branding_theme_fk
  FOREIGN KEY (org_id, branding_theme_id) REFERENCES branding_themes (org_id, id);

-- Tax-inclusive credit notes read the typed line amount as already containing GST.
ALTER TABLE customer_credit_notes
  ADD COLUMN amounts_are text NOT NULL DEFAULT 'exclusive' CHECK (amounts_are IN ('exclusive', 'inclusive'));
