import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";

// Honest status for features that need outside accounts or services. Nothing here pretends to be connected.
const ITEMS = [
  { name: "Email sending (invoices, quotes, login codes)", status: "Not connected", detail: "Documents are downloaded as PDF and sent manually. Login codes depend on the configured mail sender, which must be verified before use." },
  { name: "Bank feeds (live bank connection)", status: "Not supported", detail: "Bank activity is imported from a statement file instead. No bank login or transfer is ever made by Tidal." },
  { name: "Google account linking", status: "Not connected", detail: "Google sign-in needs the Google client registration to be confirmed against the live site before it can be marked working." },
  { name: "InvoiceNow (peppol e-invoicing)", status: "Not supported", detail: "Requires an accredited access point and IRAS onboarding. Tidal does not send InvoiceNow documents." },
];

export default function IntegrationsPage() {
  return (
    <>
      <PageHeader title="Integrations" description="Services that need an outside account. Each shows its real status." />
      <PreviewNotice>Nothing on this page is connected unless its status says so.</PreviewNotice>
      <div className="table-scroll">
        <table className="doc-table">
          <caption className="sr-only">Integration status</caption>
          <thead><tr><th scope="col">Integration</th><th scope="col">Status</th><th scope="col">What to know</th></tr></thead>
          <tbody>
            {ITEMS.map((i) => (
              <tr key={i.name}>
                <th scope="row">{i.name}</th>
                <td><span className="badge">{i.status}</span></td>
                <td className="long-text">{i.detail}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}
