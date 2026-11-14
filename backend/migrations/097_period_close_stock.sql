-- Period close also locks dated stock. Journals were guarded in 022; stock movements were not, so an
-- adjustment or stocktake could still change a closed month's quantities.
CREATE FUNCTION stock_period_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE closed date;
BEGIN
  SELECT closed_through INTO closed FROM period_closes WHERE org_id=NEW.org_id;
  IF closed IS NOT NULL AND NEW.movement_date <= closed THEN
    RAISE EXCEPTION 'period closed through %', closed USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stock_movements_period_guard BEFORE INSERT ON stock_movements FOR EACH ROW EXECUTE FUNCTION stock_period_guard();

-- Opening-stock adjustments and stocktake lines carry their own dates; guard those too so the refusal
-- arrives before the movement insert.
CREATE FUNCTION stock_adjustment_period_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE closed date;
BEGIN
  SELECT closed_through INTO closed FROM period_closes WHERE org_id=NEW.org_id;
  IF closed IS NOT NULL AND NEW.adjustment_date <= closed THEN
    RAISE EXCEPTION 'period closed through %', closed USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER stock_adjustments_period_guard BEFORE INSERT ON stock_adjustments FOR EACH ROW EXECUTE FUNCTION stock_adjustment_period_guard();
