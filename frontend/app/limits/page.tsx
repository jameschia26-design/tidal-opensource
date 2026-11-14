// One page that states every deliberate limit before people rely on the app (R28, R30, R32).
export const metadata = { title: "What Tidal does not do" };

const LIMITS: [string, string][] = [
  ["Bank statements come from CSV files only", "There is no live bank feed and no bank login. You import a statement file; matching is manual."],
  ["Tidal never moves money", "Recording a payment records money already paid or received. It does not send money, start a transfer or collect from a customer. Collections are manual."],
  ["One stock pool per company", "There are no warehouse, bin, serial or batch locations. Quantity and average cost are tracked for the company as a whole."],
  ["No employee expense claims", "You record spend the business already paid. There is no claim, approval or reimbursement workflow."],
  ["SGD only", "Every amount is Singapore dollars. Imports refuse other currencies and there is no revaluation."],
  ["InvoiceNow is not supported", "Tidal does not transmit invoices to InvoiceNow/Peppol. If your customer or the IRAS cohort rules require it, use an accredited access point. See https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/gst-invoicing-requirements/invoicenow"],
  ["Tax tools are worksheets, not filed returns", "GST F5 drafts and corporate or personal tax worksheets help an accountant prepare a filing. Nothing is filed with IRAS or ACRA and there is no XBRL output. The draft F5 is labelled as a draft."],
  ["No separate approval step", "Owners, admins, accountants and members can post documents and record payments; viewers cannot. There is no second-approver control in this release."],
  ["Deliberately limited GST", "Only Singapore GST is handled, with standard-rated, zero-rated, exempt and out-of-scope codes. Partial exemption apportionment is not calculated."],
  ["Documents print Latin text only", "Customer names or descriptions written in Chinese, Tamil or another non-Latin script print as \"?\" on PDFs. Keep a Latin-script name on the document if it must print, or attach the original file. An embedded Unicode font is the upgrade path."],
  ["Statutory tax years are rule-based, not verified against every IRAS example", "Corporate and personal tax worksheets follow the published IRAS rule tables for each year, but before filing, an accountant should check them against that year's official worked example."],
  ["No payroll, no live integrations", "People costs and third-party connections are outside this release. Foreign currency is limited to a rate on invoices and bills; there are no foreign bank accounts, no revaluation and no FX gain or loss."],
];

export default function LimitsPage() {
  return (
    <main className="page">
      <h1>What Tidal does not do</h1>
      <p>These limits are deliberate. Knowing them up front is safer than discovering them after the books depend on them.</p>
      <dl>
        {LIMITS.map(([title, text]) => (
          <div key={title}>
            <dt><strong>{title}</strong></dt>
            <dd>{text}</dd>
          </div>
        ))}
      </dl>
      <p><a href="/signup">Create an account</a> · <a href="/login">Sign in</a></p>
    </main>
  );
}
