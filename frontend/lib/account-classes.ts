// Chart-of-accounts classes, grouped under the broad account type the ledger posts with.
// The type keeps every report and posting rule unchanged; the class is the finer chart split and
// drives the chart tabs. Defaults copy the backend's 120 backfill rules so both sides agree.
import { defaultAccountClass } from "./migrate.ts";

export type AccountClassOption = { value: string; label: string; group: string };

export const ACCOUNT_CLASSES: AccountClassOption[] = [
  { value: "bank", label: "Bank", group: "asset" },
  { value: "current_asset", label: "Current Asset", group: "asset" },
  { value: "fixed_asset", label: "Fixed Asset", group: "asset" },
  { value: "inventory", label: "Inventory", group: "asset" },
  { value: "prepayment", label: "Prepayment", group: "asset" },
  { value: "non_current_asset", label: "Non-current Asset", group: "asset" },
  { value: "current_liability", label: "Current Liability", group: "liability" },
  { value: "non_current_liability", label: "Non-current Liability", group: "liability" },
  { value: "equity", label: "Equity", group: "equity" },
  { value: "retained_earnings", label: "Retained Earnings", group: "equity" },
  { value: "sales", label: "Sales", group: "revenue" },
  { value: "revenue", label: "Revenue", group: "revenue" },
  { value: "other_income", label: "Other Income", group: "revenue" },
  { value: "cost_of_sales", label: "Cost of Sales", group: "expense" },
  { value: "expense", label: "Expense", group: "expense" },
  { value: "other_expense", label: "Other Expense", group: "expense" },
];

const GROUP_BY_CLASS: Record<string, string> = Object.fromEntries(ACCOUNT_CLASSES.map(c => [c.value, c.group]));

export function classGroup(value: string): string | null {
  return GROUP_BY_CLASS[value] ?? null;
}

export function classLabel(value: string): string {
  return ACCOUNT_CLASSES.find(c => c.value === value)?.label ?? value;
}

// Only the classes inside the chosen broad type are offered, so a form can never send a mismatch.
export function classesFor(type: string): AccountClassOption[] {
  return ACCOUNT_CLASSES.filter(c => c.group === type);
}

// Standard chart tabs and ordering.
export const CLASS_TABS: { id: string; label: string; groups: string[] }[] = [
  { id: "all", label: "All", groups: [] },
  { id: "assets", label: "Assets", groups: ["asset"] },
  { id: "liabilities", label: "Liabilities", groups: ["liability"] },
  { id: "equity", label: "Equity", groups: ["equity"] },
  { id: "expenses", label: "Expenses", groups: ["expense"] },
  { id: "revenue", label: "Revenue", groups: ["revenue"] },
];

export function classInTab(value: string, tabId: string): boolean {
  const tab = CLASS_TABS.find(t => t.id === tabId);
  if (!tab || tab.groups.length === 0) return true;
  return tab.groups.includes(classGroup(value) ?? "");
}

export { defaultAccountClass };
