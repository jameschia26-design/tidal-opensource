-- Bank rules can carry several conditions on separate rows. Every condition on a rule must match
-- (Xero's behaviour) before the rule suggests its account. Fields limited to the columns a CSV
-- statement actually carries: description and reference. The rule's own description text stays as
-- the first, required condition so older rules keep matching.
CREATE TABLE bank_rule_conditions (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  rule_id bigint NOT NULL,
  field text NOT NULL CHECK (field IN ('description', 'reference')),
  value text NOT NULL CHECK (length(trim(value)) BETWEEN 1 AND 100),
  FOREIGN KEY (org_id, rule_id) REFERENCES bank_rules(org_id, id) ON DELETE CASCADE
);
CREATE INDEX bank_rule_conditions_rule_idx ON bank_rule_conditions (org_id, rule_id);
