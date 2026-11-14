"use client";
// Settings groups: Sales, Purchases, Accounting, Contacts and Projects.
// Tidal keeps the same split as landing pages that link to the real screen for each item,
// and states plainly which items are deliberately not built.
import PageHeader from "@/components/PageHeader";

type Row = { label: string; href?: string; status: string };
type Section = { title: string; description: string; rows: Row[] };

const SECTIONS: Record<string, Section> = {
  sales: {
    title: "Sales settings",
    description: "Invoice defaults, branding and statements.",
    rows: [
      { label: "Branding themes and company logo", href: "/settings/branding", status: "Theme name and footer, plus the company logo printed on every PDF." },
      { label: "Customer statements", href: "/sales/statements", status: "Activity statements emailed from the statements screen; printed from any posted invoice." },
      { label: "Online payments", status: "Not built: no card or wallet provider is connected, so invoices print without a pay-now link." },
      { label: "Invoice settings: Default Settings, Payment Services, Invoice Reminders, Sales Preferences", status: "Not built: Tidal has no per-document defaults editor, no payment services and no email reminder scheduler. Invoice numbering and tax come from the invoice form." },
    ],
  },
  purchases: {
    title: "Purchases settings",
    description: "Bills, purchase orders and expense capture.",
    rows: [
      { label: "Bills", href: "/purchases", status: "Draft, post and pay bills; per-line items and accounts." },
      { label: "Purchase orders", href: "/purchases/orders", status: "Orders and receives; orders never post to the books." },
      { label: "Expenses", href: "/purchases/expenses", status: "Records spend already paid, with receipt files. No approval or claim workflow." },
      { label: "Business network", status: "Not built: direct network invoicing between companies has no Tidal equivalent." },
      { label: "Expense settings", status: "Not built: the expense-claims module (users, limits, approvals) is out of scope; bills and expenses cover the same spend." },
    ],
  },
  accounting: {
    title: "Accounting settings",
    description: "Chart of accounts, tax, tracking and opening balances.",
    rows: [
      { label: "Chart of accounts", href: "/accounting?tab=accounts", status: "Accounts, types, watchlist and expense-claim flags." },
      { label: "Tracking categories", href: "/settings/extras", status: "Reporting tags act as one tracking category; multiple categories are not built." },
      { label: "Conversion balances", href: "/settings/conversion-balances", status: "Opening balances, with an optional comparative set." },
      { label: "Financial settings: year end, lock dates, time zone", href: "/accounting/period-close", status: "Fiscal year-end is selected when the company is created and then locked. Company time zone is configurable. Period close locks finished months." },
      { label: "Financial settings: tax basis, tax ID, tax period, default treatments", href: "/settings/tax", status: "Dated Singapore rate catalogue (7/8/9%), per-account default rate and the single GST control account." },
      { label: "GST returns", href: "/tax/gst", status: "Return list by tax period, ledger F5, and a filing state that records who filed it and when. Singapore GST only; imports, reverse charge and bad debt are catalogue-only." },
      { label: "Currencies", href: "/settings/currencies", status: "Currency table with dated rates; documents default to the rate on their own date. Month-end revaluation and realised FX post to dedicated gain and loss accounts." },
    ],
  },
  contacts: {
    title: "Contacts settings",
    description: "How contacts are stored and imported.",
    rows: [
      { label: "All contacts", href: "/contacts", status: "One list across customers and suppliers." },
      { label: "Import contacts", href: "/import", status: "CSV or text file, preview then complete, optional clearing of existing values." },
      { label: "Contact groups", status: "Not built: Tidal uses reporting tags and salespeople instead of contact groups." },
      { label: "Custom contact links", status: "Not built: no per-contact link fields beyond the website and social fields on the contact form." },
    ],
  },
  projects: {
    title: "Projects settings",
    description: "Projects, time and staff cost.",
    rows: [
      { label: "All projects", href: "/time/projects", status: "Group a customer's quotes, invoices and expenses." },
      { label: "Time entries", href: "/time/entries", status: "Record time against a project." },
      { label: "Staff time overview", href: "/time/staff", status: "Recorded hours per person." },
      { label: "Staff cost rates", href: "/time/staff-rates", status: "Hourly cost per person, with change history." },
    ],
  },
};

export default function SettingsSection({ section }: { section: string }) {
  const s = SECTIONS[section];
  if (!s) return <><PageHeader title="Settings" /><p>Unknown settings section. <a href="/settings">Back to settings</a>.</p></>;
  return <>
    <PageHeader title={s.title} description={s.description} actions={<a className="button-secondary" href="/settings">All settings</a>} />
    <div className="table-scroll"><table className="doc-table"><caption>{s.title}</caption>
      <thead><tr><th>Setting</th><th>Where it lives</th><th>Status</th></tr></thead>
      <tbody>{s.rows.map((r) => <tr key={r.label}>
        <td>{r.label}</td>
        <td>{r.href ? <a href={r.href}>Open</a> : <span className="badge">Not built</span>}</td>
        <td>{r.status}</td>
      </tr>)}</tbody>
    </table></div>
  </>;
}
