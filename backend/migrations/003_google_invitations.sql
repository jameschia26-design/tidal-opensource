-- Invitations are email allow-list entries consumed by the first Google sign-in. No token is emailed.
ALTER TABLE invitations DROP COLUMN token_hash;
CREATE INDEX oauth_states_expiry_idx ON oauth_states (expires_at);
