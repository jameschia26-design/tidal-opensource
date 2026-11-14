import Link from "next/link";
import styles from "./landing.module.css";

const capabilities = [
  {
    number: "01",
    tag: "SALES & REVENUE",
    title: "Tax invoices, quotes & receipts.",
    description: "Generate compliant Singapore tax invoices with 9% GST, convert quotes to invoices with one click, and issue credit notes and sales receipts.",
    bullets: ["Tax invoices with 9% Singapore GST", "Quote-to-invoice conversion", "Customer credit notes & refunds", "Recurring invoice automation"],
  },
  {
    number: "02",
    tag: "PURCHASES & EXPENSES",
    title: "Vendor bills, orders & expenses.",
    description: "Track vendor bills, issue purchase orders, and record billable expenses directly assignable to customer invoices.",
    bullets: ["Purchase orders & vendor profiles", "Billable customer expense tracking", "Vendor credit notes & adjustments", "Expense receipts & categorization"],
  },
  {
    number: "03",
    tag: "SINGAPORE TAX & COMPLIANCE",
    title: "IRAS-ready GST & tax worksheets.",
    description: "Automated calculations for IRAS GST Form 5 boxes, historical rate support (9%, 8%, 7%), and corporate income tax worksheets.",
    bullets: ["GST Form 5 box 1 to box 8 figures", "Standard-rated, zero-rated & exempt", "Form C-S tax worksheet preparation", "Full immutable audit ledger"],
  },
  {
    number: "04",
    tag: "FINANCIAL REPORTING",
    title: "Real-time ledger & statements.",
    description: "Complete double-entry accounting engine with instant Profit & Loss, Balance Sheet, and Trial Balance reports.",
    bullets: ["Real-time Profit & Loss statement", "Balance Sheet & Trial Balance", "General Ledger transaction breakdown", "Multi-entity company switcher"],
  },
];

const highlights = [
  {
    title: "Singapore GST Native",
    detail: "Full 9% standard rate, zero-rated exports, and exempt supplies with automated IRAS F5 filing figures.",
  },
  {
    title: "Quote to Invoice",
    detail: "Convert approved quotes into tax invoices instantly. All line items, accounts, and tax rates carry over seamlessly.",
  },
  {
    title: "Double-Entry Books",
    detail: "Real-time Trial Balance, P&L, and Balance Sheet kept balanced with an immutable general ledger.",
  },
  {
    title: "Multi-Entity Switching",
    detail: "Manage multiple Singapore companies or test accounts from one login with instant organization switching.",
  },
];

function Arrow({ diagonal = false }: { diagonal?: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <path
        d={diagonal ? "M6 18 18 6M6 6h12v12" : "M4 12h15m-6-6 6 6-6 6"}
        stroke="currentColor"
        strokeWidth="1.75"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 20 20" fill="none" aria-hidden="true" className={styles.checkSvg}>
      <circle cx="10" cy="10" r="9" stroke="currentColor" strokeWidth="1.5" />
      <path d="m6 10 2.5 2.5L14 7.5" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function Brand() {
  return (
    <span className={styles.brand}>
      <svg width="34" height="34" viewBox="0 0 40 40" fill="none" aria-hidden="true">
        <path d="M4 13h12c8 0 12 14 20 14M4 21h9c9 0 12-14 23-14M4 29h9c11 0 13-14 23-14" stroke="currentColor" strokeWidth="3" />
      </svg>
      <span>
        tidal<span className={styles.brandDot}>.</span>
      </span>
    </span>
  );
}

export default function Landing() {
  return (
    <div className={styles.page}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>

      <header className={styles.header}>
        <div className={styles.bar}>
          <Link href="/" aria-label="Tidal home" className={styles.home}>
            <Brand />
          </Link>
          <nav aria-label="Primary" className={styles.nav}>
            <a href="#features" className={styles.navLink}>
              Features
            </a>
            <a href="#singapore-gst" className={styles.navLink}>
              Singapore GST
            </a>
            <a href="https://github.com/jameschia26-design/tidal-opensource" className={styles.signIn}>
              Open source
            </a>
            <Link href="/signup" className={styles.navCta}>
              Try the demo <Arrow diagonal />
            </Link>
          </nav>
        </div>
      </header>

      <main id="main">
        {/* Hero Section */}
        <section className={styles.hero} aria-labelledby="hero-title">
          <div className={styles.heroCopy}>
            <div className={styles.pillBadge}>
              <span className={styles.pillDot} />
              <span>FREE, OPEN-SOURCE ACCOUNTING FOR SMALL BUSINESSES</span>
            </div>
            <h1 id="hero-title">
              Your books.<br />
              <em>Your control.</em>
            </h1>
            <p className={styles.lead}>
              Tidal is free accounting software built to support small companies. Use the hosted demo, or self-host it on infrastructure your organization operates—without a Tidal subscription or handing your books to a hosted accounting platform. Your hosting provider may still access infrastructure it manages.
            </p>

            <div className={styles.actions}>
              <Link href="/signup" className={styles.primary}>
                Try the demo <Arrow />
              </Link>
              <a href="https://github.com/jameschia26-design/tidal-opensource" className={styles.secondaryLink}>
                Get the free source <Arrow diagonal />
              </a>
            </div>

            <div className={styles.trustRow}>
              <span className={styles.trustItem}>
                <CheckIcon /> 9% Singapore GST
              </span>
              <span className={styles.trustItem}>
                <CheckIcon /> Double-entry ledger
              </span>
              <span className={styles.trustItem}>
                <CheckIcon /> Self-hostable
              </span>
            </div>
          </div>

          {/* Product UI Preview Card */}
          <div className={styles.previewContainer} aria-label="Tidal product preview">
            <div className={styles.mockWindow}>
              <div className={styles.windowHeader}>
                <div className={styles.windowControls}>
                  <span className={styles.dotClose} />
                  <span className={styles.dotMin} />
                  <span className={styles.dotMax} />
                </div>
                <div className={styles.windowTitle}>Tidal · Marina Tech Pte. Ltd. (SGD)</div>
                <div className={styles.windowStatus}>
                  <span className={styles.liveBadge}>IRAS 9% GST</span>
                </div>
              </div>

              <div className={styles.windowBody}>
                {/* Metric Summary */}
                <div className={styles.metricRow}>
                  <div className={styles.metricCard}>
                    <span className={styles.metricLabel}>Total Receivables (SGD)</span>
                    <span className={styles.metricValue}>$42,850.00</span>
                    <span className={styles.metricChange}>+12.4% this quarter</span>
                  </div>
                  <div className={styles.metricCard}>
                    <span className={styles.metricLabel}>Net GST Output (F5 Box 6)</span>
                    <span className={styles.metricValue}>$3,856.50</span>
                    <span className={styles.metricStatus}>Verified in balance</span>
                  </div>
                </div>

                {/* Sample Invoice Card */}
                <div className={styles.invoiceCard}>
                  <div className={styles.invoiceHeader}>
                    <div>
                      <span className={styles.invoiceType}>TAX INVOICE</span>
                      <span className={styles.invoiceNumber}>INV-2026-0042</span>
                    </div>
                    <span className={styles.statusPaid}>PAID</span>
                  </div>

                  <div className={styles.invoiceMeta}>
                    <div>
                      <span className={styles.metaLabel}>CUSTOMER</span>
                      <span className={styles.metaValue}>Alpha Horizon Pte. Ltd.</span>
                      <span className={styles.metaSub}>UEN: 202318492G</span>
                    </div>
                    <div className={styles.metaRight}>
                      <span className={styles.metaLabel}>DATE</span>
                      <span className={styles.metaValue}>31 Oct 2026</span>
                    </div>
                  </div>

                  <div className={styles.invoiceTable}>
                    <div className={styles.tableHead}>
                      <span>Description</span>
                      <span className={styles.colQty}>Qty</span>
                      <span className={styles.colPrice}>Price (SGD)</span>
                      <span className={styles.colTax}>GST</span>
                      <span className={styles.colTotal}>Amount (SGD)</span>
                    </div>
                    <div className={styles.tableRow}>
                      <div>
                        <strong>Software Architecture &amp; Engineering</strong>
                        <span className={styles.rowSub}>Sprint 4 Deliverables</span>
                      </div>
                      <span className={styles.colQty}>1</span>
                      <span className={styles.colPrice}>$4,500.00</span>
                      <span className={styles.colTax}>9% ($405)</span>
                      <span className={styles.colTotal}>$4,500.00</span>
                    </div>
                    <div className={styles.tableRow}>
                      <div>
                        <strong>Managed Cloud Operations</strong>
                        <span className={styles.rowSub}>Monthly Service Agreement</span>
                      </div>
                      <span className={styles.colQty}>1</span>
                      <span className={styles.colPrice}>$1,200.00</span>
                      <span className={styles.colTax}>9% ($108)</span>
                      <span className={styles.colTotal}>$1,200.00</span>
                    </div>
                  </div>

                  <div className={styles.invoiceTotals}>
                    <div className={styles.totalLine}>
                      <span>Subtotal</span>
                      <span>$5,700.00 SGD</span>
                    </div>
                    <div className={styles.totalLine}>
                      <span>Singapore GST (9.00%)</span>
                      <span>$513.00 SGD</span>
                    </div>
                    <div className={styles.grandTotalLine}>
                      <span>Total Due</span>
                      <span>$6,213.00 SGD</span>
                    </div>
                  </div>
                </div>

                <div className={styles.windowFooter}>
                  <span>✓ Immutable general ledger updated</span>
                  <span>GST Box 1: $5,700.00 · GST Box 6: $513.00</span>
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* Feature Highlights Ribbon */}
        <section className={styles.stripSection} aria-label="Tidal core advantages">
          <div className={styles.stripGrid}>
            {highlights.map((h) => (
              <div key={h.title} className={styles.stripCard}>
                <div className={styles.stripTitle}>{h.title}</div>
                <div className={styles.stripDetail}>{h.detail}</div>
              </div>
            ))}
          </div>
        </section>

        {/* Core Capabilities Section */}
        <section id="features" className={styles.possibilities} aria-labelledby="features-title">
          <div className={styles.sectionIntro}>
            <div className={styles.eyebrow}>DESIGNED FOR SINGAPORE SMES</div>
            <h2 id="features-title">
              Complete accounting.<br />
              <em>Zero clutter.</em>
            </h2>
            <p>
              Free, open-source tools for the everyday work of a small business. Run Tidal yourself to choose where your accounting database is hosted and who administers it.
            </p>
          </div>

          <div className={styles.capabilityGrid}>
            {capabilities.map((item) => (
              <article className={styles.capabilityCard} key={item.number}>
                <div className={styles.cardHeader}>
                  <span className={styles.cardNumber}>{item.number}</span>
                  <span className={styles.cardTag}>{item.tag}</span>
                </div>
                <h3 className={styles.cardTitle}>{item.title}</h3>
                <p className={styles.cardDesc}>{item.description}</p>
                <ul className={styles.bulletList}>
                  {item.bullets.map((b) => (
                    <li key={b} className={styles.bulletItem}>
                      <span className={styles.bulletDot} />
                      <span>{b}</span>
                    </li>
                  ))}
                </ul>
              </article>
            ))}
          </div>
        </section>

        {/* Singapore GST & Compliance Deep Dive */}
        <section id="singapore-gst" className={styles.complianceSection} aria-labelledby="compliance-title">
          <div className={styles.complianceInner}>
            <div className={styles.complianceCopy}>
              <div className={styles.eyebrowLight}>SINGAPORE STATUTORY COMPLIANCE</div>
              <h2 id="compliance-title">
                Built specifically for<br />
                <em>IRAS requirements.</em>
              </h2>
              <p>
                Singapore taxation requires exact calculations, proper tax categorization, and clean audit records. Tidal handles current and historical rates seamlessly.
              </p>
              <div className={styles.complianceList}>
                <div className={styles.complianceItem}>
                  <strong>9% Standard Rate with Historical Support</strong>
                  <span>Correctly computes 9% (from 2024), 8% (2023), and 7% rates based on document and supply dates.</span>
                </div>
                <div className={styles.complianceItem}>
                  <strong>Automated GST Form 5 Reporting</strong>
                  <span>Line items feed directly into Box 1 (Standard-rated supplies), Box 2 (Zero-rated), Box 3 (Exempt), Box 5 (Taxable purchases), and Box 6/7 net tax figures.</span>
                </div>
                <div className={styles.complianceItem}>
                  <strong>Optional GST by Document</strong>
                  <span>Flexibility for non-GST registered entities, while giving registered companies granular control over line-by-line tax application.</span>
                </div>
              </div>
            </div>

            <div className={styles.complianceCard}>
              <div className={styles.worksheetHeader}>
                <span>IRAS GST F5 WORKSHEET SUMMARY</span>
                <span className={styles.worksheetTag}>Q3 2026</span>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 1: Total Standard-Rated Supplies</span>
                <strong>$128,450.00</strong>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 2: Total Zero-Rated Supplies</span>
                <strong>$32,100.00</strong>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 3: Total Exempt Supplies</span>
                <strong>$0.00</strong>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 4: Total Supplies (Boxes 1 + 2 + 3)</span>
                <strong>$160,550.00</strong>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 5: Total Taxable Purchases</span>
                <strong>$44,200.00</strong>
              </div>
              <div className={styles.worksheetDivider} />
              <div className={styles.worksheetRow}>
                <span>Box 6: Output Tax Due</span>
                <strong>$11,560.50</strong>
              </div>
              <div className={styles.worksheetRow}>
                <span>Box 7: Input Tax Claimed</span>
                <strong>$3,978.00</strong>
              </div>
              <div className={styles.worksheetNet}>
                <span>Box 8: Net GST to Pay to IRAS</span>
                <strong className={styles.netHighlight}>$7,582.50 SGD</strong>
              </div>
            </div>
          </div>
        </section>

        {/* Closing Call to Action */}
        <section className={styles.closing} aria-labelledby="closing-title">
          <div className={styles.closingContent}>
            <div className={styles.closingTop}>
              <span className={styles.closingPill}>FREE AND OPEN SOURCE</span>
              <span className={styles.closingArrow} aria-hidden="true">
                ↗
              </span>
            </div>
            <div className={styles.closingMain}>
              <h2 id="closing-title">
                Accounting for small teams.<br />
                <em>On your terms.</em>
              </h2>
              <div className={styles.closingActions}>
                <a href="https://github.com/jameschia26-design/tidal-opensource" className={styles.lightCta}>
                  Explore the source <Arrow diagonal />
                </a>
                <Link href="/signup" className={styles.ghostCta}>
                  Try the demo
                </Link>
              </div>
            </div>
            <p className={styles.previewNote}>
              The hosted version is a demo, not for real company books. Want to deploy Tidal for your organization? Contact the project administrator through the project repository for support. Tidal's source is free; your own hosting and operations may have costs.
            </p>
          </div>
        </section>
      </main>

      {/* Footer */}
      <footer className={styles.footer}>
        <div className={styles.footerInner}>
          <div className={styles.footerBrand}>
            <Link href="/" aria-label="Tidal home" className={styles.home}>
              <Brand />
            </Link>
            <p className={styles.footerTagline}>Free, open-source accounting software built to support small companies.</p>
          </div>
          <div className={styles.footerLinks}>
            <a href="#features">Features</a>
            <a href="#singapore-gst">Singapore GST</a>
            <a href="https://github.com/jameschia26-design/tidal-opensource">Source code</a>
            <Link href="/signup">Try the demo</Link>
          </div>
          <div className={styles.footerBottom}>
            <span>Singapore · {new Date().getFullYear()} Tidal. All rights reserved.</span>
          </div>
        </div>
      </footer>
    </div>
  );
}
