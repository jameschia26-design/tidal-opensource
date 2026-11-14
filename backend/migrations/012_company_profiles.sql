ALTER TABLE organizations
  ADD COLUMN legal_name text NOT NULL DEFAULT '' CHECK (length(legal_name) <= 200),
  ADD COLUMN business_type text NOT NULL DEFAULT '' CHECK (business_type IN ('', 'company', 'sole_proprietor', 'partnership', 'llp', 'other')),
  ADD COLUMN industry text NOT NULL DEFAULT '' CHECK (length(industry) <= 100),
  ADD COLUMN contact_email text NOT NULL DEFAULT '' CHECK (length(contact_email) <= 254),
  ADD COLUMN phone text NOT NULL DEFAULT '' CHECK (length(phone) <= 40),
  ADD COLUMN website text NOT NULL DEFAULT '' CHECK (length(website) <= 300),
  ADD COLUMN gst_number text NOT NULL DEFAULT '' CHECK (length(gst_number) <= 30),
  ADD COLUMN address jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(address) = 'object'),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
