ALTER TABLE deals
  ADD COLUMN contact_id bigint,
  ADD COLUMN expected_close_date date,
  ADD COLUMN probability integer NOT NULL DEFAULT 0 CHECK (probability BETWEEN 0 AND 100),
  ADD COLUMN deal_type text NOT NULL DEFAULT '' CHECK (length(deal_type) <= 100),
  ADD COLUMN source text NOT NULL DEFAULT '' CHECK (length(source) <= 100),
  ADD COLUMN next_step text NOT NULL DEFAULT '' CHECK (length(next_step) <= 1000),
  ADD COLUMN notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
  ADD FOREIGN KEY (org_id, contact_id) REFERENCES contacts(org_id, id);
