-- Browser binding for Google sign-in: the state row stores only a hash of the cookie value.
-- Existing unbound rows (if any) are removed so no unbound state can be used.
DELETE FROM oauth_states;
ALTER TABLE oauth_states ADD COLUMN binding_hash bytea NOT NULL;
