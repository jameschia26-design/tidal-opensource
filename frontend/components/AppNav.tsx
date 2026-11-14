"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { NAV, NAV_LINKS, QUICK_CREATE, ACCOUNT_LINKS } from "@/lib/nav";
import { api } from "@/lib/api";

// Top bar: group menus (not links), one open at a time, Escape/outside click closes.
// Unbuilt items are shown disabled with a reason rather than linking to a 404.
function useMenu() {
  const [open, setOpen] = useState<string | null>(null);
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    const away = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) setOpen(null); };
    const esc = (e: KeyboardEvent) => { if (e.key === "Escape" && ref.current?.querySelector<HTMLButtonElement>('button[aria-expanded="true"]')) { ref.current.querySelector<HTMLButtonElement>('button[aria-expanded="true"]')?.focus(); setOpen(null); } };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => { document.removeEventListener("mousedown", away); document.removeEventListener("keydown", esc); };
  }, []);
  return { open, setOpen, ref };
}

const base = (href: string) => href.split("#")[0].split("?")[0];

export default function AppNav() {
  const pathname = usePathname();
  const { open, setOpen, ref } = useMenu();
  const [admin, setAdmin] = useState(false);
  const [search, setSearch] = useState("");
  // Admin links show only for platform admins; the backend enforces access on every admin route.
  useEffect(() => { api<{ is_platform_admin: boolean }>("/v1/me").then((m) => setAdmin(m.is_platform_admin)).catch(() => setAdmin(false)); }, []);
  useEffect(() => {
    const sync = () => setSearch(window.location.search);
    sync();
    window.addEventListener("popstate", sync);
    return () => window.removeEventListener("popstate", sync);
  }, [pathname]);
  useEffect(() => setOpen(null), [pathname, setOpen]);
  const all = [...NAV.flatMap((g) => g.items.map((i) => ({ g: g.label, i }))), ...NAV_LINKS.map((i) => ({ g: i.label, i }))].filter((x) => x.i.available && x.i.href);
  const tabOk = (href: string) => !href.includes("?") || href.endsWith(search);
  const hit = all.filter((x) => (pathname === base(x.i.href) || pathname.startsWith(base(x.i.href) + "/")) && tabOk(x.i.href))
    .sort((a, b) => base(b.i.href).length + (b.i.href.includes("?") ? 1 : 0) - base(a.i.href).length - (a.i.href.includes("?") ? 1 : 0))[0];
  const activeGroup = hit?.g;

  return (
    <nav aria-label="Main" className="topnav" ref={ref}>
      <ul className="topnav-groups">
        {NAV_LINKS.map((item) => <li key={item.href} className="topnav-group"><Link className="topnav-toggle" href={item.href} aria-current={activeGroup === item.label ? "true" : undefined}>{item.label}</Link></li>)}
        {NAV.map((group) => {
          const id = `menu-${group.label.replace(/\W+/g, "-").toLowerCase()}`;
          const expanded = open === group.label;
          const row = (item: (typeof group.items)[number]) => (
            <li key={item.label}>
              {item.available && item.href ? (
                <Link href={item.href} onClick={() => setOpen(null)} aria-current={hit?.i === item ? "page" : undefined}>{item.label}</Link>
              ) : (
                <span className="nav-disabled" aria-disabled="true">
                  {item.label} <span className="badge">{item.note ? "Not offered" : "Not yet available"}</span>
                  {item.note && <span className="visually-hidden"> {item.note}</span>}
                </span>
              )}
            </li>
          );
          return (
            <li key={group.label} className="topnav-group">
              {group.items.length === 1 && group.items[0].available ? (
                <Link className="topnav-toggle" href={group.items[0].href} aria-current={activeGroup === group.label ? "true" : undefined}>{group.label}</Link>
              ) : (
                <>
                  <button type="button" className="topnav-toggle" aria-expanded={expanded} aria-controls={id}
                    aria-current={activeGroup === group.label ? "true" : undefined}
                    onClick={() => setOpen(expanded ? null : group.label)}>{group.label}<span aria-hidden="true"> ▾</span></button>
                  <ul id={id} hidden={!expanded} className="topnav-menu">
                    {group.items.map(row)}
                  </ul>
                </>
              )}
            </li>
          );
        })}
      </ul>
      <div className="topnav-right">
        <div className="topnav-group">
          <button type="button" className="topnav-toggle topnav-plus" aria-label="Shortcuts" aria-expanded={open === "+"} aria-controls="menu-quick-create" onClick={() => setOpen(open === "+" ? null : "+")}>+</button>
          <ul id="menu-quick-create" hidden={open !== "+"} className="topnav-menu topnav-menu-right">
            <li className="nav-disabled">Shortcuts to common tasks and pages</li>
            {QUICK_CREATE.map((q) => (
              <li key={q.label}>
                {q.available ? <Link href={q.href} onClick={() => setOpen(null)}>{q.label}</Link>
                  : <span className="nav-disabled" aria-disabled="true">{q.label} <span className="badge">Not yet available</span></span>}
              </li>
            ))}
          </ul>
        </div>
        <div className="topnav-group">
          <button type="button" className="topnav-toggle" aria-expanded={open === "acct"} aria-controls="menu-account" onClick={() => setOpen(open === "acct" ? null : "acct")}>Account<span aria-hidden="true"> ▾</span></button>
          <ul id="menu-account" hidden={open !== "acct"} className="topnav-menu topnav-menu-right">
            {ACCOUNT_LINKS.filter((a) => admin || !a.href.startsWith("/admin")).map((a) => (
              <li key={a.label}>
                {a.available ? <Link href={a.href} onClick={() => setOpen(null)}>{a.label}</Link>
                  : <span className="nav-disabled" aria-disabled="true">{a.label} <span className="badge">Not yet available</span></span>}
              </li>
            ))}
            <li><form method="post" action="/api/v1/auth/logout"><button type="submit" className="topnav-logout">Log out</button></form></li>
          </ul>
        </div>
      </div>
    </nav>
  );
}
