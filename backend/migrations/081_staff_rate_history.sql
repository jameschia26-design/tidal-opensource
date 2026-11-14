-- Every rate change keeps its old value so a cost report can show when the rate moved.
CREATE TABLE staff_rate_history (
  org_id bigint NOT NULL REFERENCES organizations(id),
  user_id bigint NOT NULL REFERENCES users(id),
  hourly_cents bigint NOT NULL CHECK (hourly_cents BETWEEN 0 AND 100000000),
  effective_at timestamptz NOT NULL DEFAULT now(),
  changed_by bigint NOT NULL REFERENCES users(id),
  id bigserial PRIMARY KEY
);
CREATE INDEX staff_rate_history_idx ON staff_rate_history(org_id,user_id,effective_at DESC);
