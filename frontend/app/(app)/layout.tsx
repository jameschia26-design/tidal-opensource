import Link from "next/link";
import AppNav from "@/components/AppNav";
import BrandLogo from "@/components/BrandLogo";
import CompanySwitcher from "@/components/CompanySwitcher";
import GlobalSearch from "@/components/GlobalSearch";
import { OrgProvider } from "@/components/OrgProvider";
import styles from "./app.module.css";

// App shell: dark top bar (brand, org, group menus, quick create, account) over a full-width page.
export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <OrgProvider>
      <div className={styles.shell}>
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
