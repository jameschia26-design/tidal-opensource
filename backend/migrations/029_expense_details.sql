ALTER TABLE expenses
  ADD COLUMN paid_through_account_id bigint,
  ADD COLUMN vendor_id bigint,
  ADD COLUMN customer_id bigint,
  ADD COLUMN items jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(items)='array'),
  ADD CONSTRAINT expense_paid_through_fk FOREIGN KEY(org_id,paid_through_account_id) REFERENCES accounts(org_id,id),
  ADD CONSTRAINT expense_vendor_fk FOREIGN KEY(org_id,vendor_id) REFERENCES contacts(org_id,id),
  ADD CONSTRAINT expense_customer_fk FOREIGN KEY(org_id,customer_id) REFERENCES contacts(org_id,id);
