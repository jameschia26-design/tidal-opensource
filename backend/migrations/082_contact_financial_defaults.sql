-- Add-contact financial details and sales/purchase defaults from the Xero map.
ALTER TABLE contacts
  ADD COLUMN fax text NOT NULL DEFAULT '' CHECK (length(fax) <= 40),
  ADD COLUMN direct_dial text NOT NULL DEFAULT '' CHECK (length(direct_dial) <= 40),
  ADD COLUMN country text NOT NULL DEFAULT '' CHECK (length(country) <= 60),
  ADD COLUMN bank_account_name text NOT NULL DEFAULT '' CHECK (length(bank_account_name) <= 100),
  ADD COLUMN bank_account_number text NOT NULL DEFAULT '' CHECK (length(bank_account_number) <= 60),
  ADD COLUMN bank_particulars text NOT NULL DEFAULT '' CHECK (length(bank_particulars) <= 100),
  ADD COLUMN sales_account_id bigint,
  ADD COLUMN purchase_account_id bigint,
  ADD COLUMN credit_limit_cents bigint NOT NULL DEFAULT 0 CHECK (credit_limit_cents BETWEEN 0 AND 100000000000000),
  ADD COLUMN block_over_limit boolean NOT NULL DEFAULT false,
  ADD FOREIGN KEY (org_id, sales_account_id) REFERENCES accounts (org_id, id),
  ADD FOREIGN KEY (org_id, purchase_account_id) REFERENCES accounts (org_id, id);
