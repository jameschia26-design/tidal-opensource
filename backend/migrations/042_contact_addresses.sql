-- Extra named addresses per customer or supplier, beyond the billing and shipping address on the contact.
CREATE TABLE contact_addresses (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL,
  contact_id bigint NOT NULL,
  label      text NOT NULL CHECK (length(trim(label)) BETWEEN 1 AND 100),
  address    jsonb NOT NULL CHECK (jsonb_typeof(address) = 'object'),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts(org_id, id)
);
CREATE INDEX contact_addresses_contact ON contact_addresses(org_id, contact_id);
