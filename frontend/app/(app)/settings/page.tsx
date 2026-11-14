"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import CompanySettings from "@/components/CompanySettings";
import { api } from "@/lib/api";

type Me = { email: string };

export default function Settings() {
  const [me, setMe] = useState<Me | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    api<Me>("/v1/me").then(setMe).catch(() => setError("Could not load your account."));
  }, []);
  return (
    <>
      <PageHeader title="Settings" description="Company profile, team and integrations." />
      <p className="notice">Before you rely on Tidal, read <a href="/limits">What Tidal does not do</a>.</p>
      {error && <p role="alert" className="form-error">{error}</p>}
      {me && <p>Signed in as <strong>{me.email}</strong></p>}
      <h2>Company profile</h2>
      <CompanySettings />
      <h2>Users</h2>
      <p><a href="/settings/users">Manage who can open this company</a> — invite by email, choose a role, cancel a pending invitation.</p>
      <h2>Tracking categories</h2>
      <p><a href="/settings/tracking">Manage tracking categories and options</a> — tag document lines and report on them. Two active categories per company.</p>
      <h2>Document numbering</h2>
      <p><a href="/settings/numbering">Set prefixes and next numbers</a> — invoices, bills, credit notes, purchase orders and quotes.</p>
      <h2>Audit history</h2>
      <p><a href="/settings/audit-history">Read or export the audit history</a> — filter by date, person or record. Owners and admins only.</p>
      <h2>Integrations</h2>
      <ul className="task-list">
        <li>
          InvoiceNow: <span className="badge">Not supported</span> — Tidal does not transmit invoices.
          Check the <a href="https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/gst-invoicing-requirements/invoicenow" target="_blank" rel="noreferrer">IRAS InvoiceNow requirements</a>.
        </li>
        <li>Bank feeds: <span className="badge">Not supported</span> — statements arrive as CSV files.</li>
        <li>Online payments: <span className="badge">Not supported</span> — recording a payment never moves money.</li>
      </ul>
      <form method="post" action="/api/v1/auth/logout" className="inline-form">
        <button type="submit" className="button-secondary">Sign out</button>
      </form>
    </>
  );
}
