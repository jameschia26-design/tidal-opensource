-- R18: password-reset requests are throttled per connecting address before any lookup or hashing.
CREATE TABLE password_reset_requests (
  id bigserial PRIMARY KEY,
  ip text NOT NULL CHECK (length(ip) BETWEEN 1 AND 100),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_requests_ip ON password_reset_requests (ip, created_at);
