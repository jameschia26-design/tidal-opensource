-- Fields from Zoho map §5 (New Vendor) and §6 (New Customer) not yet stored.
ALTER TABLE contacts
  ADD COLUMN salutation text NOT NULL DEFAULT '' CHECK (length(salutation) <= 20),
  ADD COLUMN language text NOT NULL DEFAULT 'en' CHECK (language IN ('en', 'zh', 'ms', 'ta')),
  ADD COLUMN currency text NOT NULL DEFAULT 'SGD' CHECK (currency = 'SGD'),
  ADD COLUMN portal_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN owner_user_id bigint REFERENCES users(id),
  ADD COLUMN opening_balance_cents bigint NOT NULL DEFAULT 0 CHECK (opening_balance_cents BETWEEN 0 AND 100000000000000),
  ADD COLUMN opening_balance_date date,
  ADD COLUMN internal_remarks text NOT NULL DEFAULT '' CHECK (length(internal_remarks) <= 4000);

CREATE TABLE contact_persons (
  id          bigserial PRIMARY KEY,
  org_id      bigint NOT NULL REFERENCES organizations(id),
  contact_id  bigint NOT NULL,
  salutation  text NOT NULL DEFAULT '' CHECK (length(salutation) <= 20),
  first_name  text NOT NULL DEFAULT '' CHECK (length(first_name) <= 100),
  last_name   text NOT NULL DEFAULT '' CHECK (length(last_name) <= 100),
  email       text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
  work_phone  text NOT NULL DEFAULT '' CHECK (length(work_phone) <= 40),
  mobile      text NOT NULL DEFAULT '' CHECK (length(mobile) <= 40),
  position    integer NOT NULL DEFAULT 0,
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id) ON DELETE CASCADE
);
CREATE INDEX contact_persons_contact_idx ON contact_persons (org_id, contact_id, position);
