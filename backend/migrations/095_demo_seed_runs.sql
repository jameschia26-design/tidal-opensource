-- Durable completion marker for the operator-run 2025 demo seed. A row appears only after the whole
-- demo passed its own checks, so a partial run can never be reported as "already there, nothing changed".
CREATE TABLE demo_seed_runs (
  org_id       bigint PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
  label        text NOT NULL,
  counts       jsonb NOT NULL DEFAULT '{}'::jsonb,
  completed_at timestamptz NOT NULL DEFAULT now()
);
