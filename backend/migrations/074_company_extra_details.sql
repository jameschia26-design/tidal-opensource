-- Remaining Organization details fields from the Xero settings inventory.
ALTER TABLE organizations
  ADD COLUMN description text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
  ADD COLUMN mobile text NOT NULL DEFAULT '' CHECK (length(mobile) <= 40),
  ADD COLUMN fax text NOT NULL DEFAULT '' CHECK (length(fax) <= 40),
  ADD COLUMN facebook text NOT NULL DEFAULT '' CHECK (length(facebook) <= 300),
  ADD COLUMN twitter text NOT NULL DEFAULT '' CHECK (length(twitter) <= 300),
  ADD COLUMN linkedin text NOT NULL DEFAULT '' CHECK (length(linkedin) <= 300),
  ADD COLUMN physical_address jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(physical_address) = 'object');
