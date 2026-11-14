import Link from "next/link";

export default function Tabs({ items, current }: { items: { label: string; href: string }[]; current: string }) {
  return (
    <nav aria-label="Section" className="tabs">
      <ul>
        {items.map((t) => (
          <li key={t.href}>
            <Link href={t.href} aria-current={current === t.href ? "page" : undefined}>{t.label}</Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}
