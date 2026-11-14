ALTER TABLE journal_lines ADD COLUMN description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000);
ALTER TABLE repeating_journal_lines ADD COLUMN description text NOT NULL DEFAULT '' CHECK (length(description) <= 2000);
