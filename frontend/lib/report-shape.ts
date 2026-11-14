// The two ledger statements arrive already grouped by account class from the API. These helpers put
// the sections and the computed lines into standard order, so the screens and the CSV
// export read the same rows.

export type ReportLine = { code: string; name: string; class?: string; amount_cents: number; computed?: boolean };
export type ReportSection = { key: string; label: string; lines: ReportLine[]; total_cents: number };

export type PnlData = {
  revenue_cents: number;
  cost_of_sales_cents: number;
  gross_profit_cents: number;
  operating_expenses_cents: number;
  operating_profit_cents: number;
  other_income_cents: number;
  other_expense_cents: number;
  expense_cents: number;
  profit_cents: number;
  sections: ReportSection[];
};

export type BalanceSheetData = {
  assets_cents: number;
  liabilities_cents: number;
  equity_cents: number;
  net_assets_cents: number;
  balanced: boolean;
  sections: ReportSection[];
};

export type ReportBlock =
  | { kind: "section"; key: string; label: string; totalLabel: string; lines: ReportLine[]; total_cents: number; previous_cents: number; previousLines: ReportLine[] }
  | { kind: "total"; key: string; label: string; amount_cents: number; previous_cents: number };

const EMPTY: ReportSection = { key: "", label: "", lines: [], total_cents: 0 };

function section(data: { sections: ReportSection[] }, key: string) {
  return data.sections.find((s) => s.key === key) ?? EMPTY;
}

function sectionBlock(data: { sections: ReportSection[] }, previous: { sections: ReportSection[] } | undefined, key: string, label: string, totalLabel: string): ReportBlock {
  const current = section(data, key);
  const before = previous ? section(previous, key) : EMPTY;
  return { kind: "section", key, label, totalLabel, lines: current.lines, total_cents: current.total_cents, previous_cents: before.total_cents, previousLines: before.lines };
}

function totalBlock(label: string, amount: number, previousAmount: number): ReportBlock {
  return { kind: "total", key: label.toLowerCase().replace(/[^a-z0-9]+/g, "-"), label, amount_cents: amount, previous_cents: previousAmount };
}

// pnlBlocks: Revenue, cost of sales, gross profit, operating expenses, operating profit, other
// income, other expense, net profit.
export function pnlBlocks(data: PnlData, previous?: PnlData): ReportBlock[] {
  const before = previous;
  return [
    sectionBlock(data, before, "revenue", "Revenue", "Total revenue"),
    sectionBlock(data, before, "cost_of_sales", "Cost of sales", "Total cost of sales"),
    totalBlock("Gross profit", data.gross_profit_cents, before?.gross_profit_cents ?? 0),
    sectionBlock(data, before, "operating_expenses", "Operating expenses", "Total operating expenses"),
    totalBlock("Operating profit", data.operating_profit_cents, before?.operating_profit_cents ?? 0),
    sectionBlock(data, before, "other_income", "Other income", "Total other income"),
    sectionBlock(data, before, "other_expense", "Other expense", "Total other expense"),
    totalBlock("Net profit", data.profit_cents, before?.profit_cents ?? 0),
  ];
}

// bsBlocks: assets by class, total assets, liabilities by class, total liabilities, equity (with the
// accumulated profit line the API appends), then net assets.
export function bsBlocks(data: BalanceSheetData, previous?: BalanceSheetData): ReportBlock[] {
  const before = previous;
  return [
    sectionBlock(data, before, "bank", "Bank", "Total bank"),
    sectionBlock(data, before, "current_assets", "Current assets", "Total current assets"),
    sectionBlock(data, before, "fixed_assets", "Fixed assets", "Total fixed assets"),
    sectionBlock(data, before, "inventory", "Inventory", "Total inventory"),
    sectionBlock(data, before, "prepayments", "Prepayments", "Total prepayments"),
    sectionBlock(data, before, "non_current_assets", "Non-current assets", "Total non-current assets"),
    totalBlock("Total assets", data.assets_cents, before?.assets_cents ?? 0),
    sectionBlock(data, before, "current_liabilities", "Current liabilities", "Total current liabilities"),
    sectionBlock(data, before, "non_current_liabilities", "Non-current liabilities", "Total non-current liabilities"),
    totalBlock("Total liabilities", data.liabilities_cents, before?.liabilities_cents ?? 0),
    sectionBlock(data, before, "equity", "Equity", "Total equity"),
    totalBlock("Net assets", data.net_assets_cents, before?.net_assets_cents ?? 0),
  ];
}

// previousAmount finds the earlier figure for a line: by code when it has one, by name for the
// computed accumulated profit line, whose code is blank.
export function previousAmount(lines: ReportLine[], line: ReportLine): number {
  const match = line.code ? lines.find((l) => l.code === line.code) : lines.find((l) => l.name === line.name);
  return match?.amount_cents ?? 0;
}
