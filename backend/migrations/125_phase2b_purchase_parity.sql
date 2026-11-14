-- Phase 2b: purchase-side form parity — bills, purchase orders, supplier credits, batch and
-- over-payments, repeating bills and credit-note currency.
--
-- Nothing is deleted and existing rows keep their meaning. Purchase order stored statuses stay
-- draft/issued/received/cancelled; "awaiting approval" and "billed" are added alongside them.

-- Bills: Singapore permit number and the planned date shown on the list.
ALTER TABLE bills
  ADD COLUMN permit_number text NOT NULL DEFAULT '' CHECK (length(permit_number) <= 50),
  ADD COLUMN planned_date date;

-- An overpayment is explicit: the payment row records that the payer chose to pay more than was
-- owed, with a description and tracking, instead of posting a silent credit note.
ALTER TABLE bill_payments
  ADD COLUMN overpayment boolean NOT NULL DEFAULT false,
  ADD COLUMN overpayment_description text NOT NULL DEFAULT '' CHECK (length(overpayment_description) <= 500),
  ADD COLUMN tracking jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tracking) = 'object');

-- Credit notes can be issued in a foreign currency, like invoices and bills.
ALTER TABLE customer_credit_notes
  ADD COLUMN currency text NOT NULL DEFAULT 'SGD' CHECK (currency ~ '^[A-Z]{3}$'),
  ADD COLUMN fx_rate_micro bigint NOT NULL DEFAULT 1000000 CHECK (fx_rate_micro >= 1000);

-- Repeating bills: what a run does with each generated bill, and whether due runs happen without
-- pressing the button. Sending stays out (no email module).
ALTER TABLE recurring_bill_profiles
  ADD COLUMN mode text NOT NULL DEFAULT 'draft' CHECK (mode IN ('draft', 'approve')),
  ADD COLUMN auto_generate boolean NOT NULL DEFAULT false;

-- Purchase orders: sent timestamp and how much of the order has been billed.
ALTER TABLE purchase_orders
  ADD COLUMN sent_at timestamptz,
  ADD COLUMN billed_cents bigint NOT NULL DEFAULT 0 CHECK (billed_cents >= 0);
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_status_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_status_check
  CHECK (status IN ('draft', 'awaiting_approval', 'issued', 'received', 'billed', 'cancelled'));

-- Supplier credits become documents in their own right: their own number, a draft state before
-- posting, and an expense account for a credit that is not attached to a bill.
ALTER TABLE supplier_credits
  ALTER COLUMN bill_id DROP NOT NULL,
  ALTER COLUMN journal_id DROP NOT NULL,
  ADD COLUMN credit_number text,
  ADD COLUMN status text NOT NULL DEFAULT 'open' CHECK (status IN ('draft', 'open', 'void')),
  ADD COLUMN account_id bigint,
  ADD COLUMN currency text NOT NULL DEFAULT 'SGD' CHECK (currency ~ '^[A-Z]{3}$'),
  ADD COLUMN fx_rate_micro bigint NOT NULL DEFAULT 1000000 CHECK (fx_rate_micro >= 1000);

UPDATE supplier_credits SET credit_number = 'SC-' || lpad(id::text, 5, '0') WHERE credit_number IS NULL;
ALTER TABLE supplier_credits ALTER COLUMN credit_number SET NOT NULL;
ALTER TABLE supplier_credits ADD CONSTRAINT supplier_credits_credit_number_check
  CHECK (length(trim(credit_number)) BETWEEN 1 AND 50 AND credit_number = trim(credit_number));
ALTER TABLE supplier_credits ADD CONSTRAINT supplier_credits_account_fk
  FOREIGN KEY (org_id, account_id) REFERENCES accounts (org_id, id);
ALTER TABLE supplier_credits ADD CONSTRAINT supplier_credits_journal_state_check
  CHECK ((status = 'draft') = (journal_id IS NULL));
CREATE UNIQUE INDEX supplier_credits_number_uq ON supplier_credits (org_id, credit_number);

ALTER TABLE document_numbering
  ADD COLUMN supplier_credit_prefix text NOT NULL DEFAULT 'SC-' CHECK (length(supplier_credit_prefix) <= 10),
  ADD COLUMN supplier_credit_next bigint NOT NULL DEFAULT 1 CHECK (supplier_credit_next >= 1);
UPDATE document_numbering SET supplier_credit_next = GREATEST(
  supplier_credit_next,
  COALESCE((SELECT MAX(id) + 1 FROM supplier_credits sc WHERE sc.org_id = document_numbering.org_id), 1));

-- Extend the one allocator to supplier credits. A typed number wins; a blank one takes the next free.
CREATE OR REPLACE FUNCTION assign_document_number() RETURNS trigger LANGUAGE plpgsql AS $$
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
    WHEN 'supplier_credits' THEN prefix_col := 'supplier_credit_prefix'; next_col := 'supplier_credit_next'; number_col := 'credit_number';
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

CREATE TRIGGER supplier_credits_number_assign BEFORE INSERT ON supplier_credits
  FOR EACH ROW EXECUTE FUNCTION assign_document_number();

-- A payment above the bill total is allowed only when the row says it is an overpayment, so a
-- mistyped amount is refused while a deliberate supplier overpayment can be recorded.
CREATE OR REPLACE FUNCTION check_bill_payment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE st text; total bigint; paid bigint; issued date;
BEGIN
  SELECT status,total_cents,bill_date INTO st,total,issued FROM bills WHERE org_id=NEW.org_id AND id=NEW.bill_id FOR UPDATE;
  SELECT COALESCE(sum(amount_cents),0) INTO paid FROM bill_payments WHERE org_id=NEW.org_id AND bill_id=NEW.bill_id;
  IF st IS DISTINCT FROM 'posted' OR NEW.payment_date<issued OR (paid>total AND NOT NEW.overpayment) THEN
    RAISE EXCEPTION 'invalid bill payment' USING ERRCODE='check_violation';
  END IF;
  RETURN NULL;
END $$;
