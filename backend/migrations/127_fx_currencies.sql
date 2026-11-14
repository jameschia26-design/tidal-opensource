-- Phase 3b multicurrency: a currency list with dated exchange rates, and the ledger tags a foreign
-- posting needs so realised and unrealised movements can be reported by currency.
--
-- Books stay in SGD. A rate is micro-units: one unit of the currency is rate_micro/1e6 SGD, the same
-- scale documents already use in fx_rate_micro (migration 118), so a rate row can default a document.

CREATE TABLE currencies (
  org_id      bigint NOT NULL REFERENCES organizations(id),
  code        text NOT NULL CHECK (code ~ '^[A-Z]{3}$'),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  is_base     boolean NOT NULL DEFAULT false,
  active      boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, code)
);
-- One base currency per company. SGD is fixed as the base, so the row is the reference for every rate.
CREATE UNIQUE INDEX currencies_one_base ON currencies (org_id) WHERE is_base;
INSERT INTO currencies (org_id, code, name, is_base)
  SELECT id, 'SGD', 'Singapore dollar', true FROM organizations;

CREATE TABLE currency_rates (
  org_id        bigint NOT NULL,
  currency_code text NOT NULL,
  rate_date     date NOT NULL,
  rate_micro    bigint NOT NULL CHECK (rate_micro BETWEEN 1000 AND 1000000000000),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, currency_code, rate_date),
  FOREIGN KEY (org_id, currency_code) REFERENCES currencies (org_id, code) ON DELETE CASCADE
);
CREATE INDEX currency_rates_lookup ON currency_rates (org_id, currency_code, rate_date DESC);

-- A journal line carries the currency of the deal it came from. Only FX postings set it today; every
-- older line stays NULL, which reads as SGD and keeps existing reports unchanged.
ALTER TABLE journal_lines ADD COLUMN currency text CHECK (currency ~ '^[A-Z]{3}$');

-- One month-end revaluation per company and period. A rerun reverses the previous journal and posts
-- the new figure, so the FX accounts hold one period's movement rather than a pile of runs.
CREATE TABLE fx_revaluation_runs (
  id                  bigserial PRIMARY KEY,
  org_id              bigint NOT NULL REFERENCES organizations(id),
  period_end          date NOT NULL,
  journal_id          bigint NOT NULL,
  reversal_journal_id bigint,
  total_cents         bigint NOT NULL DEFAULT 0,
  created_by          bigint NOT NULL REFERENCES users(id),
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, period_end),
  FOREIGN KEY (org_id, journal_id) REFERENCES journals (org_id, id),
  FOREIGN KEY (org_id, reversal_journal_id) REFERENCES journals (org_id, id)
);
