ALTER TABLE contacts ADD COLUMN account_number text NOT NULL DEFAULT '' CHECK (length(account_number) <= 50);
