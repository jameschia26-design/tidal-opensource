-- Login and signup request limits enforced in the database, so they hold across replicas.
-- send_log is one row per emailed code; cooldown and hourly cap count these rows under the user row lock.
CREATE TABLE login_send_log (
  id         bigserial PRIMARY KEY,
  user_id    bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_send_log_user_idx ON login_send_log (user_id, created_at);

-- Signup requests per connecting address. X-Forwarded-For is believed only when the connection
-- comes from loopback or a proxy listed in TIDAL_TRUSTED_PROXIES (see clientIP in auth.go).
CREATE TABLE signup_requests (
  id         bigserial PRIMARY KEY,
  ip         text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signup_requests_ip_idx ON signup_requests (ip, created_at);

-- At most one active (unused, unexpired) challenge per user, enforced by the database.
CREATE UNIQUE INDEX login_challenges_one_active ON login_challenges (user_id) WHERE used_at IS NULL;
