-- Company time zone, shown on financial settings. Dates remain the document's own dates; the zone
-- is what a user reads as "today" in the UI.
ALTER TABLE organizations ADD COLUMN timezone text NOT NULL DEFAULT 'Asia/Singapore' CHECK (length(timezone) BETWEEN 1 AND 64);
