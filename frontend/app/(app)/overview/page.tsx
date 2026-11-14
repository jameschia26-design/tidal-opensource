"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import HomeWidgets from "@/components/HomeWidgets";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";

type Watch = { id: number; code: string; name: string; type: string; balance_cents: number };
const WORDS: Record<string, string> = { asset: "Asset", liability: "Liability", equity: "Equity", revenue: "Revenue", expense: "Expense" };

export default function Overview() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} role={selected?.role} />;
}

function Workspace({ orgId, role }: { orgId: number | undefined; role: string | undefined }) {
  const [rows, setRows] = useState<Watch[] | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<{ accounts: Watch[] }>(`/v1/reports/watchlist?org_id=${orgId}`)
      .then(r => { if (live) setRows(r.accounts); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  return (
    <>
      <PageHeader title="Dashboard" description="What is happening in this company, up to today." />
      {orgId != null && <HomeWidgets orgId={orgId} />}
      <h2>Areas</h2>
      <ul className="task-list">
        <li><Link href="/sales">Sales</Link> — invoices, quotes, customers and receipts</li>
        <li><Link href="/purchases">Purchases</Link> — bills, purchase orders, suppliers and expenses</li>
        <li><Link href="/accounting">Accounting</Link> — chart of accounts, journals and period close</li>
        <li><Link href="/tax/gst">Tax</Link> — GST returns, settings and rates</li>
        <li><Link href="/contacts">Contacts</Link> — customers and suppliers</li>
        <li><Link href="/time/projects">Projects</Link> — tasks, members, estimates and time</li>
      </ul>
      <ul className="task-list">
        <li><Link href="/onboarding">Set up your company profile and UEN</Link></li>
        <li><Link href="/sales/customers">Add your first customer</Link></li>
        <li><Link href="/tax/gst">Check your GST status</Link></li>
      </ul>
      <h2>Watchlist</h2>
      {orgId == null && <p className="notice">Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {orgId != null && rows == null && !error && <p role="status">Loading balances…</p>}
      {rows != null && rows.length === 0 && <p className="notice">No accounts on the watchlist. Open an account under Accounting and tick &ldquo;Show on dashboard watchlist&rdquo;.{(role !== "viewer") && " "}</p>}
      {rows != null && rows.length > 0 && <div className="table-scroll"><table className="doc-table"><caption className="sr-only">Watchlist accounts</caption><thead><tr><th scope="col">Account</th><th scope="col">Type</th><th scope="col" className="num">Balance</th></tr></thead><tbody>{rows.map(a => <tr key={a.id}><th scope="row">{a.code} · {a.name}</th><td>{WORDS[a.type] ?? a.type}</td><td className="num">{formatCents(a.balance_cents)}</td></tr>)}</tbody></table></div>}
      <p className="muted small">Balances are posted ledger movements up to today, so future-dated postings and unmatched statement lines are left out. Accounts show here only when their watchlist option is on.</p>
    </>
  );
}
