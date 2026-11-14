"use client";
import Link from "next/link";
import { useOrg } from "./OrgProvider";

// Real switcher: choosing a company changes the org_id sent with each company request.
export default function CompanySwitcher() {
  const { orgs, selected, error, select } = useOrg();

  if (error) return <p className="muted small">Company list unavailable.</p>;
  if (!orgs) return <p className="muted small">Loading company…</p>;
  if (orgs.length === 0) {
    return <p className="muted small">No company yet. <Link href="/onboarding">Set up your company</Link></p>;
  }
  return (
    <label className="switcher">
      <span className="switcher-label">Company / books</span>
      <select value={selected ? String(selected.id) : ""} onChange={(e) => {
        const hit = orgs.find((o) => String(o.id) === e.target.value);
        if (hit) select(hit.id);
      }} disabled={orgs.length === 1}>
        {orgs.map((o) => <option key={String(o.id)} value={String(o.id)}>{o.is_book ? `${o.company_name} / ${o.name}` : `${o.name} / Main books`}</option>)}
      </select>
    </label>
  );
}
