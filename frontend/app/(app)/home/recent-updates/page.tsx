import PageHeader from "@/components/PageHeader";

// ponytail: hand-kept list. Move to a table only if updates must be published without a release.
const UPDATES = [
  { date: "2026-10-29", text: "Company events list under Billing." },
  { date: "2026-10-29", text: "Bills can post purchase discounts to a chosen expense account." },
  { date: "2026-10-29", text: "Payment drafts: the 200 draft limit now holds when several saves happen at once." },
  { date: "2026-10-29", text: "Sales receipts accept item SKU scanning." },
  { date: "2026-10-29", text: "Payables aging can be viewed as of an earlier date." },
];

export default function RecentUpdatesPage() {
  return <>
    <PageHeader title="Recent updates" description="What changed in Tidal." />
    <ul>{UPDATES.map(u => <li key={u.text}><time dateTime={u.date}>{u.date}</time> — {u.text}</li>)}</ul>
  </>;
}
