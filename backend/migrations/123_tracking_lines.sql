-- Phase 1e: tracking categories and per-LINE tracking values.
--
-- Xero allows two active categories per organisation, each with named options, and the value is
-- chosen per document line (invoice, bill, credit note, journal line), not per document. A line's
-- values are stored as {"<category_id>": <option_id>} so it can carry one option from each category.
-- The reports read posted journal lines, so a line split across two options is counted by amount
-- rather than the whole document landing on one option.
--
-- Existing document-level tags (entity_extras.tags, keyed by reporting_tags id with the option name as
-- the value) are copied to line level only where the document carries exactly one tag: that mapping is
-- unambiguous. A document with two or more tags is left unmapped and counted in
-- tracking_backfill_report, because guessing which line belongs to which option would misstate the
-- tracking summary. Document-level tags stay where they are; nothing is deleted.

CREATE TABLE tracking_categories (
  id         bigserial PRIMARY KEY,
  org_id     bigint NOT NULL REFERENCES organizations(id),
  name       text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  active     boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX tracking_categories_name ON tracking_categories (org_id, lower(name));

CREATE TABLE tracking_options (
  id          bigserial PRIMARY KEY,
  org_id      bigint NOT NULL,
  category_id bigint NOT NULL,
  name        text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  archived    boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, category_id) REFERENCES tracking_categories (org_id, id)
);
CREATE UNIQUE INDEX tracking_options_name ON tracking_options (category_id, lower(name));

-- Two active categories per company, as Xero allows. Archiving one frees the slot; values already on
-- lines keep pointing at it so history and reports do not change.
CREATE FUNCTION tracking_category_limit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE active_count int;
BEGIN
  IF NOT NEW.active THEN
    RETURN NEW;
  END IF;
  SELECT count(*) INTO active_count FROM tracking_categories
    WHERE org_id = NEW.org_id AND active AND id <> NEW.id;
  IF active_count >= 2 THEN
    RAISE EXCEPTION 'a company can have at most two active tracking categories' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER tracking_categories_limit BEFORE INSERT OR UPDATE ON tracking_categories
  FOR EACH ROW EXECUTE FUNCTION tracking_category_limit();

-- Per-line values. Object, not array: the category id is the key, so a second category needs no new
-- column and an option id can never be read as the wrong category.
ALTER TABLE invoice_lines ADD COLUMN tracking jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tracking) = 'object');
ALTER TABLE bill_lines ADD COLUMN tracking jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tracking) = 'object');
ALTER TABLE journal_lines ADD COLUMN tracking jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tracking) = 'object');

CREATE TABLE tracking_backfill_report (
  kind              text PRIMARY KEY,
  documents_mapped  bigint NOT NULL,
  documents_skipped bigint NOT NULL,
  lines_mapped      bigint NOT NULL,
  ran_at            timestamptz NOT NULL DEFAULT now()
);

-- tracking_backfill copies legacy document tags onto lines. It is idempotent and kept as a function so
-- the migration and the tests run the same SQL.
CREATE FUNCTION tracking_backfill() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  -- 1. Every reporting tag becomes a category and its option list becomes options. They start
  --    inactive so the two-active rule below can decide which two keep the slot.
  INSERT INTO tracking_categories (org_id, name, active)
    SELECT DISTINCT rt.org_id, rt.name, false FROM reporting_tags rt
    ON CONFLICT (org_id, lower(name)) DO NOTHING;

  INSERT INTO tracking_options (org_id, category_id, name)
    SELECT rt.org_id, tc.id, o.value
    FROM reporting_tags rt
    JOIN tracking_categories tc ON tc.org_id = rt.org_id AND lower(tc.name) = lower(rt.name)
    CROSS JOIN LATERAL jsonb_array_elements_text(rt.options) AS o(value)
    ON CONFLICT (category_id, lower(name)) DO NOTHING;

  -- 2. Two categories stay active, oldest first. The trigger refuses a third.
  UPDATE tracking_categories tc SET active = r.rn <= 2
    FROM (SELECT id, row_number() OVER (PARTITION BY org_id ORDER BY id) AS rn FROM tracking_categories) r
    WHERE r.id = tc.id AND tc.active IS DISTINCT FROM (r.rn <= 2);

  -- 3. Invoices: a document with exactly one non-blank tag maps every one of its lines to that option.
  --    Posted lines are immutable, so their guard is off for this metadata-only update; amounts and
  --    descriptions are not touched, and the whole migration runs in one transaction.
  ALTER TABLE invoice_lines DISABLE TRIGGER invoice_lines_guard;
  WITH single AS (
    SELECT e.org_id, e.entity_id AS doc_id, c.id AS category_id, o.id AS option_id
    FROM entity_extras e
    JOIN LATERAL (SELECT count(*) AS n, min(key) AS tag, min(value) AS val FROM jsonb_each_text(e.tags) WHERE value <> '') t ON t.n = 1
    JOIN reporting_tags rt ON rt.org_id = e.org_id AND rt.id::text = t.tag
    JOIN tracking_categories c ON c.org_id = rt.org_id AND lower(c.name) = lower(rt.name)
    JOIN tracking_options o ON o.category_id = c.id AND o.name = t.val
    WHERE e.entity_kind = 'invoices'
  )
  UPDATE invoice_lines l SET tracking = jsonb_build_object(s.category_id::text, s.option_id)
    FROM single s WHERE l.org_id = s.org_id AND l.invoice_id = s.doc_id AND l.tracking = '{}';
  -- The deferred total check has to run before the guard can be switched back on.
  SET CONSTRAINTS ALL IMMEDIATE;
  ALTER TABLE invoice_lines ENABLE TRIGGER invoice_lines_guard;

  -- 4. Bills: same rule.
  ALTER TABLE bill_lines DISABLE TRIGGER bill_lines_guard;
  WITH single AS (
    SELECT e.org_id, e.entity_id AS doc_id, c.id AS category_id, o.id AS option_id
    FROM entity_extras e
    JOIN LATERAL (SELECT count(*) AS n, min(key) AS tag, min(value) AS val FROM jsonb_each_text(e.tags) WHERE value <> '') t ON t.n = 1
    JOIN reporting_tags rt ON rt.org_id = e.org_id AND rt.id::text = t.tag
    JOIN tracking_categories c ON c.org_id = rt.org_id AND lower(c.name) = lower(rt.name)
    JOIN tracking_options o ON o.category_id = c.id AND o.name = t.val
    WHERE e.entity_kind = 'bills'
  )
  UPDATE bill_lines l SET tracking = jsonb_build_object(s.category_id::text, s.option_id)
    FROM single s WHERE l.org_id = s.org_id AND l.bill_id = s.doc_id AND l.tracking = '{}';
  SET CONSTRAINTS ALL IMMEDIATE;
  ALTER TABLE bill_lines ENABLE TRIGGER bill_lines_guard;

  -- 5. The posted journal lines of those documents carry the same option, which is what the tracking
  --    summary and the P&L tracking columns read. journal_lines is append-only, so the trigger is
  --    switched off for this metadata backfill only; amounts, accounts and dates are not touched, and
  --    the whole migration runs in one transaction.
  ALTER TABLE journal_lines DISABLE TRIGGER journal_lines_append_only;
  UPDATE journal_lines jl SET tracking = l.tracking
    FROM invoice_lines l JOIN invoices i ON i.org_id = l.org_id AND i.id = l.invoice_id
    WHERE jl.org_id = i.org_id AND jl.journal_id = i.journal_id AND l.tracking <> '{}' AND jl.tracking = '{}';
  UPDATE journal_lines jl SET tracking = l.tracking
    FROM bill_lines l JOIN bills b ON b.org_id = l.org_id AND b.id = l.bill_id
    WHERE jl.org_id = b.org_id AND jl.journal_id = b.journal_id AND l.tracking <> '{}' AND jl.tracking = '{}';
  ALTER TABLE journal_lines ENABLE TRIGGER journal_lines_append_only;

  -- 6. What the backfill did, so the skipped documents are a number rather than a guess.
  DELETE FROM tracking_backfill_report;
  INSERT INTO tracking_backfill_report (kind, documents_mapped, documents_skipped, lines_mapped)
  SELECT 'invoices',
    count(*) FILTER (WHERE n = 1), count(*) FILTER (WHERE n > 1),
    (SELECT count(*) FROM invoice_lines WHERE tracking <> '{}')
  FROM (SELECT (SELECT count(*) FROM jsonb_each_text(e.tags) WHERE value <> '') AS n
        FROM entity_extras e WHERE e.entity_kind = 'invoices') t;
  INSERT INTO tracking_backfill_report (kind, documents_mapped, documents_skipped, lines_mapped)
  SELECT 'bills',
    count(*) FILTER (WHERE n = 1), count(*) FILTER (WHERE n > 1),
    (SELECT count(*) FROM bill_lines WHERE tracking <> '{}')
  FROM (SELECT (SELECT count(*) FROM jsonb_each_text(e.tags) WHERE value <> '') AS n
        FROM entity_extras e WHERE e.entity_kind = 'bills') t;
END $$;

SELECT tracking_backfill();
