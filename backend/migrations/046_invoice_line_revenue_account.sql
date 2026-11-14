-- Optional income account per invoice line (subscription plan, setup fee and addon accounts). NULL = Sales.
ALTER TABLE invoice_lines
  ADD COLUMN revenue_account_id bigint,
  ADD FOREIGN KEY (org_id, revenue_account_id) REFERENCES accounts(org_id, id);
