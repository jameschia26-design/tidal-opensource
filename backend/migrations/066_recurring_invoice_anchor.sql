-- Existing schedules retain their currently scheduled date as their future anchor.
ALTER TABLE recurring_templates ADD COLUMN anchor_date date;
UPDATE recurring_templates SET anchor_date = next_run;
ALTER TABLE recurring_templates ALTER COLUMN anchor_date SET NOT NULL;
