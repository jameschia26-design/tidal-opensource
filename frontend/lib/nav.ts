// Navigation groups the app's pages. Groups are menus, not links. `available` is true only when the page exists; `note` explains a deliberate gap.
export type NavItem = { label: string; href: string; available: boolean; note?: string };
export type NavGroup = { label: string; items: NavItem[] };
export const NAV_LINKS: NavItem[] = [{ label: "Import", href: "/import", available: true }];

export const NAV: NavGroup[] = [
  { label: "Home", items: [{ label: "Home", href: "/overview", available: true }, { label: "Getting started", href: "/home/getting-started", available: true }, { label: "Recent updates", href: "/home/recent-updates", available: true }, { label: "Notifications", href: "/notifications", available: true }] },
  {
    label: "Sales",
    items: [
      { label: "Sales overview", href: "/sales", available: true },
      { label: "Invoices", href: "/sales/invoices", available: true },
      { label: "Online payments", href: "/sales/online-payments", available: false, note: "No payment provider connected" },
      { label: "Quotes", href: "/sales/quotes", available: true },
      { label: "Customer statements", href: "/sales/statements", available: true },
      { label: "Recurring invoices", href: "/sales/recurring", available: true },
      { label: "Payments received", href: "/sales/payments", available: true },
      { label: "Sales receipts", href: "/sales/receipts", available: true },
      { label: "Credit notes", href: "/sales/credit-notes", available: true },
    ],
  },
  {
    label: "Purchases",
    items: [
      { label: "Purchases overview", href: "/purchases/overview", available: true },
      { label: "Bills", href: "/purchases", available: true },
      { label: "Purchase orders", href: "/purchases/orders", available: true },
      { label: "Expenses", href: "/purchases/expenses", available: true },
      { label: "Recurring bills", href: "/purchases/recurring-bills", available: true },
      { label: "Recurring expenses", href: "/purchases/recurring", available: true },
      { label: "Receipts inbox", href: "/purchases/expenses/receipts-inbox", available: true },
      { label: "Payments made", href: "/purchases/payments", available: true },
      { label: "Purchase receives", href: "/purchases/receives", available: true },
    ],
  },
  {
    label: "Reporting",
    items: [
      { label: "All reports", href: "/reports", available: true },
      { label: "Analytics", href: "/reports/analytics", available: true },
      { label: "Dashboards", href: "/reports/dashboards", available: true },
      { label: "Cash flow manager", href: "/reports/cash-flow-manager", available: true },
      { label: "Visualizations", href: "/reports/visualizations", available: true },
      { label: "Business health scorecard", href: "/reports/scorecard", available: true },
      { label: "Profit and Loss", href: "/reports/profit-loss", available: true },
      { label: "Aging report", href: "/reports/aging", available: true },
      { label: "Foreign currency gains and losses", href: "/reports/fx-gains-losses", available: true },
    ],
  },
  {
    label: "Accounting",
    items: [
      { label: "Short-term cash flow", href: "/accounting/cash-flow", available: true },
      { label: "Business snapshot", href: "/accounting/snapshot", available: true },
      { label: "Bank accounts", href: "/banking/accounts", available: true },
      { label: "Reconcile", href: "/banking/reconcile", available: true },
      { label: "Bank rules", href: "/banking/rules", available: true },
      { label: "Chart of accounts", href: "/accounting?tab=accounts", available: true },
      { label: "Fixed assets", href: "/accounting/assets", available: true },
      { label: "Manual journals", href: "/accounting?tab=journals", available: true },
      { label: "Repeating journals", href: "/accounting/repeating-journals", available: true },
      { label: "Transaction locking", href: "/accounting/period-close", available: true },
      { label: "Currencies", href: "/settings/currencies", available: true },
    ],
  },
  {
    label: "Tax",
    items: [
      { label: "GST returns", href: "/tax/gst", available: true },
      { label: "Company income tax", href: "/tax/income/cit", available: true },
      { label: "Personal income tax", href: "/tax/income/pit", available: true },
    ],
  },
  {
    label: "Contacts",
    items: [
      { label: "All contacts", href: "/contacts", available: true },
      { label: "Customers", href: "/sales/customers", available: true },
      { label: "Suppliers", href: "/purchases/suppliers", available: true },
    ],
  },
  {
    label: "Items",
    items: [{ label: "Items and services", href: "/items", available: true }],
  },
  {
    label: "Organisation",
    items: [
      { label: "Company documents", href: "/documents", available: true },
      { label: "Opening balances", href: "/settings/conversion-balances", available: true },
      { label: "Branding themes", href: "/settings/branding", available: true },
    ],
  },
  {
    label: "Billing",
    items: [
      { label: "Subscriptions", href: "/billing/subscriptions", available: true },
      { label: "Plans, addons and coupons", href: "/billing/catalog", available: true },
      { label: "Pricing widget setup", href: "/billing/pricing-widgets", available: true },
      { label: "Subscription events", href: "/billing/events", available: true },
    ],
  },
  {
    label: "CRM",
    items: [
      { label: "Leads", href: "/crm", available: true },
      { label: "Deals", href: "/crm/deals", available: true },
    ],
  },
  {
    label: "Projects",
    items: [
      { label: "All projects", href: "/time/projects", available: true },
      { label: "Time entries", href: "/time/entries", available: true },
      { label: "Staff time overview", href: "/time/staff", available: true },
      { label: "Staff cost rates", href: "/time/staff-rates", available: true },
    ],
  },
];

// Quick-create "+" menu.
export type QuickCreate = { label: string; href: string; available: boolean };
export const QUICK_CREATE: QuickCreate[] = [
  { label: "Invoice", href: "/sales/invoices/new", available: true },
  { label: "Bills page", href: "/purchases", available: true },
  { label: "Contacts page", href: "/contacts", available: true },
  { label: "Quotes page", href: "/sales/quotes", available: true },
  { label: "Purchase orders page", href: "/purchases/orders", available: true },
  { label: "Documents page", href: "/documents", available: true },
  { label: "Manual journals page", href: "/accounting?tab=journals", available: true },
  { label: "Spend money", href: "/banking/reconcile", available: true },
  { label: "Receive money", href: "/banking/reconcile", available: true },
  { label: "Transfer money", href: "/banking/transfer", available: true },
];

// User menu.
export type AccountLink = { label: string; href: string; available: boolean };
export const ACCOUNT_LINKS: AccountLink[] = [
  { label: "Settings", href: "/settings", available: true },
  { label: "Subscription and billing", href: "/settings/subscription", available: false },
  { label: "Add new organisation", href: "/onboarding", available: true },
  { label: "Profile", href: "/settings/profile", available: true },
  { label: "Account", href: "/settings/account", available: true },
  { label: "Company invitations", href: "/invitations", available: true },
  { label: "Users", href: "/admin/users", available: true },
  { label: "Invitations", href: "/admin/invitations", available: true },
];

// Contextual tabs for sales pages.
export const SALES_TABS = [
  { label: "Quotes", href: "/sales/quotes" },
  { label: "Invoices", href: "/sales/invoices" },
  { label: "Recurring", href: "/sales/recurring" },
  { label: "Payments received", href: "/sales/payments" },
  { label: "Sales receipts", href: "/sales/receipts" },
  { label: "Credit notes", href: "/sales/credit-notes" },
];
