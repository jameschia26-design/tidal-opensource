-- The deferred total check on bills must count line GST now that a bill total is net plus GST.
CREATE OR REPLACE FUNCTION check_bill_total() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE doc bigint; total bigint; amount bigint; n int;
BEGIN
  IF TG_TABLE_NAME='bills' THEN doc:=NEW.id;
  ELSIF TG_OP='DELETE' THEN doc:=OLD.bill_id;
  ELSE doc:=NEW.bill_id; END IF;
  SELECT total_cents INTO total FROM bills WHERE id=doc;
  SELECT COALESCE(sum(amount_cents),0)+COALESCE(sum(gst_cents),0),count(*) INTO amount,n FROM bill_lines WHERE bill_id=doc;
  IF n<1 OR amount<>total THEN
    RAISE EXCEPTION 'bill total does not match lines' USING ERRCODE='check_violation';
  END IF;
  RETURN NULL;
END $$;
