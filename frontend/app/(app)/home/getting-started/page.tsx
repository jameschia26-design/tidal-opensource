"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api } from "@/lib/api";

const STEPS = [
  { key: "company", label: "Set up your company", href: "/settings", note: "Name, financial year, tax registration." },
  { key: "accounts", label: "Review the chart of accounts", href: "/accounting?tab=accounts", note: "Add or rename accounts you need." },
  { key: "contacts", label: "Add customers and vendors", href: "/sales/customers", note: "Vendors are under Purchases." },
  { key: "items", label: "Add items", href: "/items", note: "Goods and services you sell or buy." },
  { key: "invoice", label: "Send your first invoice", href: "/sales/invoices", note: "Create, post, then download the PDF." },
  { key: "payment", label: "Record a payment", href: "/sales/payments", note: "Apply it to open invoices." },
  { key: "bill", label: "Enter a bill", href: "/purchases", note: "Post it, then record the payment under Payments made." },
  { key: "reports", label: "Check reports", href: "/reports", note: "Aging and the other reports update as you post." },
];

export default function GettingStartedPage() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [done, setDone] = useState<Record<string, boolean>>({});
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    api<{ steps: { key: string; done: boolean }[] }>(`/v1/setup-progress?org_id=${orgId}`)
      .then(r => { if (live) setDone(Object.fromEntries(r.steps.map(s => [s.key, s.done]))); })
      .catch(() => { if (live) setDone({}); });
    return () => { live = false; };
  }, [orgId]);
  const count = STEPS.filter(s => done[s.key]).length;
  return <>
    <PageHeader title="Getting started" description="The usual first steps for a new company." />
    {orgId && <p className="section-help" role="status">{count} of {STEPS.length} steps done.</p>}
    <ol className="checklist">{STEPS.map(s => <li key={s.key}><Link href={s.href}>{s.label}</Link> <span>{s.note}</span> {done[s.key] ? <span className="badge">Done</span> : <span className="badge">To do</span>}</li>)}</ol>
  </>;
}
