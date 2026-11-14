-- Optional per-line contra-expense account for a line's discount. A line's own account wins over the bill's.
ALTER TABLE bill_lines ADD COLUMN discount_account_id bigint;
ALTER TABLE bill_lines ADD FOREIGN KEY (org_id, discount_account_id) REFERENCES accounts(org_id, id);
