-- Phase 5 collaboration: per-member document-posting permission, login history, chargeable time and a
-- one-per-user running timer.
ALTER TABLE memberships ADD COLUMN permissions jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(permissions) = 'object');

ALTER TABLE time_entries ADD COLUMN billable boolean NOT NULL DEFAULT false;
ALTER TABLE time_entries ADD COLUMN rate_cents bigint NOT NULL DEFAULT 0 CHECK (rate_cents >= 0);

CREATE TABLE login_events (
  id bigserial PRIMARY KEY,
  user_id bigint NOT NULL REFERENCES users(id),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  ip text NOT NULL DEFAULT '' CHECK (length(ip) <= 64),
  user_agent text NOT NULL DEFAULT '' CHECK (length(user_agent) <= 300)
);
CREATE INDEX login_events_user_time ON login_events (user_id, occurred_at DESC);
CREATE TRIGGER login_events_append_only BEFORE UPDATE OR DELETE ON login_events
  FOR EACH ROW EXECUTE FUNCTION forbid_change();

-- One running timer per user. Stopping it writes a normal time entry, so reports keep reading entries.
CREATE TABLE time_timers (
  user_id bigint PRIMARY KEY REFERENCES users(id),
  org_id bigint NOT NULL REFERENCES organizations(id),
  project_id bigint NOT NULL,
  task text NOT NULL CHECK (length(trim(task)) BETWEEN 1 AND 200),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 1000),
  billable boolean NOT NULL DEFAULT false,
  rate_cents bigint NOT NULL DEFAULT 0 CHECK (rate_cents >= 0),
  started_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, project_id) REFERENCES projects(org_id, id)
);
