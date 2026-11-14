-- Xero's New user form captures a first and last name with the email and role.
-- Names are stored on the invitation so the first sign-in can fill the user record.
ALTER TABLE invitations
  ADD COLUMN first_name text NOT NULL DEFAULT '' CHECK (length(first_name) <= 100),
  ADD COLUMN last_name  text NOT NULL DEFAULT '' CHECK (length(last_name) <= 100);
