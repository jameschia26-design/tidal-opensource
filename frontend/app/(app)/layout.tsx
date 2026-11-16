import Link from "next/link";
import AppNav from "@/components/AppNav";
import BrandLogo from "@/components/BrandLogo";
import CompanySwitcher from "@/components/CompanySwitcher";
import GlobalSearch from "@/components/GlobalSearch";
import { OrgProvider } from "@/components/OrgProvider";
import styles from "./app.module.css";

// App shell: dark top bar (brand, org, group menus, quick create, account) over a full-width page.
export default function AppLayout({ children }: { children: React.ReactNode }) {
  const isDemo = process.env.NEXT_PUBLIC_DEMO_MODE === "1";
  return (
    <OrgProvider>
      <div className={styles.shell}>
        {isDemo && (
          <aside aria-label="Demo environment notice" style={{ background: "#f59e0b", color: "#000", padding: "0.35rem 1rem", fontSize: "0.8rem", textAlign: "center", fontWeight: 500 }}>
            Tidal Public Demo · All data is purged 7 days after creation. Want to self-host for your team? <a href="mailto:admin@goodsoil.cc" style={{ textDecoration: "underline", color: "#000" }}>Contact admin</a> or see <a href="https://github.com/jameschia26-design/tidal-opensource" target="_blank" rel="noreferrer" style={{ textDecoration: "underline", color: "#000" }}>source code</a>.
          </aside>
        )}
        <a className="skip-link" href="#app-main">Skip to content</a>
        <header className={styles.topbar}>
          <Link href="/overview" className={styles.brand} aria-label="Tidal home"><BrandLogo /></Link>
          <div className={styles.org}><CompanySwitcher /></div>
          <GlobalSearch />
          <AppNav />
        </header>
        <main id="app-main" className={styles.main} tabIndex={-1}>{children}</main>
      </div>
    </OrgProvider>
  );
}
