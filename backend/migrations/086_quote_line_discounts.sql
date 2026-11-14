-- Quotes could only store a plain quantity times price line, so a per-line discount was refused by
-- the database even though the form offered one. The rule now allows an optional discount_basis_points
-- key and requires the stored amount to be the discounted value, rounded half up to the cent, which
-- is the same arithmetic the invoice builder uses.
CREATE OR REPLACE FUNCTION valid_quote_lines(items jsonb, total bigint) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
  SELECT jsonb_typeof(items)='array' AND jsonb_array_length(items) BETWEEN 1 AND 200
    AND count(*)=jsonb_array_length(items)
    AND COALESCE(bool_and(line_no IS NOT NULL AND description IS NOT NULL AND quantity IS NOT NULL AND unit_price_cents IS NOT NULL AND amount_cents IS NOT NULL
      AND COALESCE(discount_basis_points,0) BETWEEN 0 AND 10000
      AND line_no BETWEEN 1 AND 200 AND length(trim(description)) BETWEEN 1 AND 200
      AND quantity BETWEEN 1 AND 1000000 AND unit_price_cents BETWEEN 0 AND 100000000000000
      AND amount_cents=(quantity*unit_price_cents*(10000-COALESCE(discount_basis_points,0))+5000)/10000),false)
    AND count(DISTINCT line_no)=count(*) AND sum(amount_cents)=total
  FROM jsonb_to_recordset(items) AS l(line_no integer,description text,quantity bigint,unit_price_cents bigint,amount_cents bigint,discount_basis_points bigint)
$$;
