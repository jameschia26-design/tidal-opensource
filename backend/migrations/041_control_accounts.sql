-- Which ledger accounts hold customer and supplier balances. No row means the standard 1100 and 2100.
CREATE TABLE control_accounts (
  org_id bigint PRIMARY KEY REFERENCES organizations(id),
  receivable_account_id bigint NOT NULL,
  payable_account_id bigint NOT NULL,
  FOREIGN KEY (org_id, receivable_account_id) REFERENCES accounts(org_id, id),
  FOREIGN KEY (org_id, payable_account_id) REFERENCES accounts(org_id, id)
);
