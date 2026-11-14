"use client";
import { useState } from "react";
import MigrationWorkspace from "@/components/MigrationWorkspace";
import PTBImport from "@/components/PTBImport";
import PageHeader from "@/components/PageHeader";
import RecordImportPanel from "@/components/RecordImportPanel";
import FullContactImport from "@/components/FullContactImport";
import ContactImport from "@/components/ContactImport";
import { useOrg } from "@/components/OrgProvider";

const TYPES = ["accounts", "customers", "vendors", "items", "bills", "invoices", "quotes", "sales receipts", "credit notes", "payments", "expenses", "recurring expenses"];
export default function ImportPage() {
  const { selected } = useOrg();
  const [kind, setKind] = useState(TYPES[0]);
  if (!selected) return <p className="notice">Choose a company to import into.</p>;
  if (selected.role === "viewer") return <p className="notice">Company viewers cannot import.</p>;
  return <div>
    <PageHeader title="Import" description="Import records from CSV." />
    <section className="record-form">
      <label htmlFor="import-kind">Record type</label>
      <select id="import-kind" value={kind} onChange={(e) => setKind(e.target.value)}>{TYPES.map((x) => <option key={x} value={x}>{x[0].toUpperCase() + x.slice(1)}</option>)}</select>
    </section>
    <RecordImportPanel kind={kind} orgId={selected.id} onDone={async () => {}} withInvoices={kind === "payments"} />
    <section aria-label="Move from another system">
      <MigrationWorkspace />
    </section>
    <PTBImport />
    <section aria-label="Customer and supplier CSV import">
      <h2>Customer and supplier CSV import</h2>
      <p className="section-help">Simple customer or supplier CSV import with per-row validation.</p>
      <ContactImport orgId={selected.id} role="customer" onDone={async () => {}} />
      <ContactImport orgId={selected.id} role="supplier" onDone={async () => {}} />
    </section>
    <section aria-label="Advanced contact import">
      <h2>Advanced contact import</h2>
      <p className="section-help">Use CSV preview, contact updates, or export tools.</p>
      <FullContactImport />
    </section>
  </div>;
}
