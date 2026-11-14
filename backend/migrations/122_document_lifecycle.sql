-- Phase 1d: document lifecycle, voiding and editable numbers.
--
-- Statuses stay additive: draft/posted rows keep their meaning, voided is new, and paid is derived
-- from allocations in the API (never typed in). Numbers are assigned when a draft starts and are
-- editable until posting; posting never renumbers a document.

ALTER TABLE invoices DROP CONSTRAINT invoices_status_check;
ALTER TABLE invoices ADD CONSTRAINT invoices_status_check
  CHECK (status IN ('draft', 'awaiting_approval', 'posted', 'voided'));
ALTER TABLE invoices DROP CONSTRAINT invoices_check1;
ALTER TABLE invoices ADD CONSTRAINT invoices_check1 CHECK (
  (status IN ('draft', 'awaiting_approval') AND number IS NULL AND journal_id IS NULL AND posted_at IS NULL)
  OR (status IN ('posted', 'voided') AND number IS NOT NULL AND journal_id IS NOT NULL AND posted_at IS NOT NULL)
);

ALTER TABLE bills DROP CONSTRAINT bills_status_check;
ALTER TABLE bills ADD CONSTRAINT bills_status_check
  CHECK (status IN ('draft', 'awaiting_approval', 'posted', 'voided'));
ALTER TABLE bills DROP CONSTRAINT bills_check1;
ALTER TABLE bills ADD CONSTRAINT bills_check1 CHECK (
  (status IN ('draft', 'awaiting_approval') AND journal_id IS NULL AND posted_at IS NULL)
  OR (status IN ('posted', 'voided') AND journal_id IS NOT NULL AND posted_at IS NOT NULL)
);

-- A void is a status change on the document; every other field stays exactly as posted.
CREATE OR REPLACE FUNCTION invoice_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'voided' THEN
    RAISE EXCEPTION 'voided invoice % is immutable', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.status = 'posted' THEN
    IF NEW.status = 'voided'
       AND (to_jsonb(NEW) - 'status') = (to_jsonb(OLD) - 'status') THEN
      RETURN NEW;
    END IF;
    IF OLD.document_number IS NULL AND NEW.document_number IS NOT NULL
       AND (to_jsonb(NEW) - 'document_number') = (to_jsonb(OLD) - 'document_number') THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'posted invoice % is immutable', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.status <> 'draft' AND NEW.document_number IS DISTINCT FROM OLD.document_number THEN
    RAISE EXCEPTION 'invoice % number is editable only as a draft', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status = 'posted' AND (NEW.number IS NULL OR NEW.journal_id IS NULL OR NEW.posted_at IS NULL
     OR NEW.org_id <> OLD.org_id OR NEW.contact_id <> OLD.contact_id OR NEW.total_cents <> OLD.total_cents
     OR NEW.invoice_date <> OLD.invoice_date OR NEW.due_date <> OLD.due_date) THEN
    RAISE EXCEPTION 'invoice % may only become posted with its number and journal', OLD.id
      USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status NOT IN ('draft', 'awaiting_approval', 'posted') THEN
    RAISE EXCEPTION 'invalid invoice status for %', OLD.id USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION bill_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'voided' THEN
    RAISE EXCEPTION 'voided bill is immutable' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.status = 'posted' THEN
    IF NEW.status = 'voided'
       AND (to_jsonb(NEW) - 'status') = (to_jsonb(OLD) - 'status') THEN
      RETURN NEW;
    END IF;
    IF OLD.document_number IS NULL AND NEW.document_number IS NOT NULL
       AND (to_jsonb(NEW) - 'document_number') = (to_jsonb(OLD) - 'document_number') THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'posted bill is immutable' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.status <> 'draft' AND NEW.document_number IS DISTINCT FROM OLD.document_number THEN
    RAISE EXCEPTION 'bill number is editable only as a draft' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status NOT IN ('draft', 'awaiting_approval', 'posted') THEN
    RAISE EXCEPTION 'invalid bill status' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF OLD.status <> NEW.status AND NOT (OLD.status IN ('draft', 'awaiting_approval') AND NEW.status IN ('awaiting_approval', 'posted')) THEN
    RAISE EXCEPTION 'invalid bill transition' USING ERRCODE = 'insufficient_privilege';
  END IF;
  IF NEW.status = 'posted' AND (NEW.contact_id <> OLD.contact_id OR NEW.supplier_reference <> OLD.supplier_reference
     OR NEW.bill_date <> OLD.bill_date OR NEW.due_date <> OLD.due_date OR NEW.notes <> OLD.notes
     OR NEW.total_cents <> OLD.total_cents) THEN
    RAISE EXCEPTION 'posting cannot change bill contents' USING ERRCODE = 'insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;

-- Voiding an open credit note is allowed; everything else on it stays frozen.
CREATE OR REPLACE FUNCTION customer_credit_note_freeze() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'credit notes cannot be deleted' USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.status = 'open' AND NEW.status = 'void'
     AND (to_jsonb(NEW) - 'status') = (to_jsonb(OLD) - 'status') THEN
    RETURN NEW;
  END IF;
  IF OLD.status <> 'draft' THEN
    RAISE EXCEPTION 'only draft credit notes can change' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;

-- A voided credit note keeps the journal it posted (and the reversal); a draft void has neither.
ALTER TABLE customer_credit_notes DROP CONSTRAINT customer_credit_notes_check1;
ALTER TABLE customer_credit_notes ADD CONSTRAINT customer_credit_notes_check1 CHECK (
  (status IN ('draft', 'void') AND journal_id IS NULL AND posted_at IS NULL)
  OR (status IN ('open', 'void') AND journal_id IS NOT NULL AND posted_at IS NOT NULL)
);

-- Reversals now also cover invoices, customer credit notes and manual journals.
ALTER TABLE document_reversals DROP CONSTRAINT document_reversals_doc_kind_check;
ALTER TABLE document_reversals ADD CONSTRAINT document_reversals_doc_kind_check
  CHECK (doc_kind IN ('bill', 'bill_payment', 'expense', 'invoice', 'customer_credit_note', 'journal'));

-- Editable user-visible document numbers. The legacy numeric invoice number stays for old clients.
ALTER TABLE invoices ADD COLUMN document_number text;
ALTER TABLE bills ADD COLUMN document_number text;
ALTER TABLE quotes ADD COLUMN quote_number text;

-- Backfilling a new number column touches rows the immutability guards protect. Disabling a trigger and
-- re-enabling it cannot happen in one transaction once rows have fired, so the guards themselves allow
-- exactly one transition: a posted or voided document whose new number column is still empty may have
-- it filled, provided nothing else changes. That is also true for the application, which never clears
-- a number, so the guards keep their purpose.
-- A sent quote is content-immutable, so the guard has to permit the one transition the backfill makes:
-- filling an empty quote number while nothing else on the row changes.
CREATE OR REPLACE FUNCTION quote_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.org_id<>OLD.org_id OR NEW.id<>OLD.id OR NEW.created_by<>OLD.created_by OR NEW.created_at<>OLD.created_at THEN
    RAISE EXCEPTION 'quote identity is immutable';
  END IF;
  IF OLD.status<>'draft' AND
    (to_jsonb(NEW)-ARRAY['status','invoice_id','updated_at']) IS DISTINCT FROM
    (to_jsonb(OLD)-ARRAY['status','invoice_id','updated_at']) THEN
    IF OLD.quote_number IS NULL AND NEW.quote_number IS NOT NULL
       AND (to_jsonb(NEW)-'quote_number') = (to_jsonb(OLD)-'quote_number') THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'sent quote content is immutable';
  END IF;
  IF NEW.status<>OLD.status AND NOT ((OLD.status='draft' AND NEW.status='sent') OR (OLD.status='sent' AND NEW.status IN ('accepted','rejected'))) THEN
    RAISE EXCEPTION 'invalid quote transition';
  END IF;
  IF OLD.invoice_id IS NOT NULL AND NEW.invoice_id IS DISTINCT FROM OLD.invoice_id THEN
    RAISE EXCEPTION 'converted quote invoice is immutable';
  END IF;
  RETURN NEW;
END $$;
UPDATE invoices SET document_number = number::text WHERE number IS NOT NULL;
UPDATE bills SET document_number = 'BILL-' || lpad(id::text, 5, '0') WHERE document_number IS NULL;
UPDATE quotes SET quote_number = 'QUO-' || lpad(id::text, 5, '0') WHERE quote_number IS NULL;

-- The backfill fires the deferred document-total checks on the rows it touches. They are evaluated now,
-- before the remaining ALTER statements, because Postgres refuses to alter a table with pending trigger
-- events; committing with them queued would leave the same rows unchecked until the end anyway.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE quotes ALTER COLUMN quote_number SET NOT NULL;
ALTER TABLE invoices ADD CONSTRAINT invoices_document_number_check
  CHECK (document_number IS NULL OR (length(trim(document_number)) BETWEEN 1 AND 50 AND document_number = trim(document_number)));
ALTER TABLE bills ADD CONSTRAINT bills_document_number_check
  CHECK (document_number IS NULL OR (length(trim(document_number)) BETWEEN 1 AND 50 AND document_number = trim(document_number)));
ALTER TABLE quotes ADD CONSTRAINT quotes_quote_number_check
  CHECK (length(trim(quote_number)) BETWEEN 1 AND 50 AND quote_number = trim(quote_number));

CREATE UNIQUE INDEX invoices_document_number_uq ON invoices(org_id, document_number) WHERE document_number IS NOT NULL;
CREATE UNIQUE INDEX bills_document_number_uq ON bills(org_id, document_number) WHERE document_number IS NOT NULL;
CREATE UNIQUE INDEX quotes_quote_number_uq ON quotes(org_id, quote_number);

-- Per-organisation, per-document-type numbering. Existing invoice counters carry over so posted
-- numbers stay gapless; other types start at 1 and the allocator skips any number already used.
CREATE TABLE document_numbering (
  org_id               bigint PRIMARY KEY REFERENCES organizations(id),
  invoice_prefix       text NOT NULL DEFAULT 'INV-' CHECK (length(invoice_prefix) <= 10),
  invoice_next         bigint NOT NULL DEFAULT 1 CHECK (invoice_next >= 1),
  bill_prefix          text NOT NULL DEFAULT 'BILL-' CHECK (length(bill_prefix) <= 10),
  bill_next            bigint NOT NULL DEFAULT 1 CHECK (bill_next >= 1),
  credit_note_prefix   text NOT NULL DEFAULT 'CN-' CHECK (length(credit_note_prefix) <= 10),
  credit_note_next     bigint NOT NULL DEFAULT 1 CHECK (credit_note_next >= 1),
  purchase_order_prefix text NOT NULL DEFAULT 'PO-' CHECK (length(purchase_order_prefix) <= 10),
  purchase_order_next  bigint NOT NULL DEFAULT 1 CHECK (purchase_order_next >= 1),
  quote_prefix         text NOT NULL DEFAULT 'QUO-' CHECK (length(quote_prefix) <= 10),
  quote_next           bigint NOT NULL DEFAULT 1 CHECK (quote_next >= 1),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

INSERT INTO document_numbering(org_id, invoice_next)
SELECT o.id, COALESCE((SELECT c.invoice_next FROM core_counters c WHERE c.org_id = o.id),
                      (SELECT COALESCE(MAX(i.number), 0) + 1 FROM invoices i WHERE i.org_id = o.id))
FROM organizations o
ON CONFLICT (org_id) DO NOTHING;

-- One allocator for every document type. A typed number wins; a blank one takes the next free
-- number, so imports and every creation path get the same behaviour.
CREATE FUNCTION assign_document_number() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  prefix_col text;
  next_col text;
  number_col text;
  candidate text;
  prefix text;
  seq bigint;
  used boolean;
BEGIN
  CASE TG_TABLE_NAME
    WHEN 'invoices' THEN prefix_col := 'invoice_prefix'; next_col := 'invoice_next'; number_col := 'document_number';
    WHEN 'bills' THEN prefix_col := 'bill_prefix'; next_col := 'bill_next'; number_col := 'document_number';
    WHEN 'quotes' THEN prefix_col := 'quote_prefix'; next_col := 'quote_next'; number_col := 'quote_number';
    WHEN 'customer_credit_notes' THEN prefix_col := 'credit_note_prefix'; next_col := 'credit_note_next'; number_col := 'credit_number';
    WHEN 'purchase_orders' THEN prefix_col := 'purchase_order_prefix'; next_col := 'purchase_order_next'; number_col := 'po_number';
  END CASE;
  IF NULLIF(btrim(COALESCE(to_jsonb(NEW) ->> number_col, '')), '') IS NOT NULL THEN
    RETURN NEW;
  END IF;
  INSERT INTO document_numbering(org_id) VALUES (NEW.org_id) ON CONFLICT (org_id) DO NOTHING;
  EXECUTE format('SELECT %I, %I FROM document_numbering WHERE org_id=$1 FOR UPDATE', prefix_col, next_col)
    INTO prefix, seq USING NEW.org_id;
  LOOP
    candidate := prefix || lpad(seq::text, 5, '0');
    EXECUTE format('SELECT EXISTS(SELECT 1 FROM %I WHERE org_id=$1 AND %I=$2)', TG_TABLE_NAME, number_col)
      INTO used USING NEW.org_id, candidate;
    EXIT WHEN NOT used;
    seq := seq + 1;
  END LOOP;
  EXECUTE format('UPDATE document_numbering SET %I=$2, updated_at=now() WHERE org_id=$1', next_col)
    USING NEW.org_id, seq + 1;
  NEW := jsonb_populate_record(NEW, jsonb_build_object(number_col, candidate));
  RETURN NEW;
END $$;

CREATE TRIGGER invoices_number_assign BEFORE INSERT ON invoices FOR EACH ROW EXECUTE FUNCTION assign_document_number();
CREATE TRIGGER bills_number_assign BEFORE INSERT ON bills FOR EACH ROW EXECUTE FUNCTION assign_document_number();
CREATE TRIGGER quotes_number_assign BEFORE INSERT ON quotes FOR EACH ROW EXECUTE FUNCTION assign_document_number();
CREATE TRIGGER customer_credit_notes_number_assign BEFORE INSERT ON customer_credit_notes FOR EACH ROW EXECUTE FUNCTION assign_document_number();
CREATE TRIGGER purchase_orders_number_assign BEFORE INSERT ON purchase_orders FOR EACH ROW EXECUTE FUNCTION assign_document_number();
