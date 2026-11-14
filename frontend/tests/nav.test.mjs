// Nav model: workflow groups, no Xero branding, no bank-account link.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { NAV, NAV_LINKS, QUICK_CREATE, ACCOUNT_LINKS, SALES_TABS } from "../lib/nav.ts";

test("workflow groups, no Xero or 'Do more with Tidal' copy, no bank-account link", () => {
  assert.equal(NAV.length, 12);
  assert.deepEqual(NAV.slice(-5, -2).map((g) => g.label), ["Items", "Organisation", "Billing"]);
  assert.deepEqual(NAV_LINKS, [{ label: "Import", href: "/import", available: true }]);
  assert.equal(NAV.find((g) => g.label === "Items")?.items[0].href, "/items");
  assert.ok(!NAV.some((g) => g.items.some((i) => i.href === "/items" && g.label !== "Items")));
  assert.equal(NAV.flatMap((g) => g.items).filter((i) => i.href === "/purchases/expenses").length, 1);
  assert.equal(NAV.flatMap((g) => g.items).filter((i) => i.href === "/contacts/import").length, 0);
  assert.match(readFileSync(new URL("../app/(app)/contacts/import/page.tsx", import.meta.url), "utf8"), /redirect\("\/import"\)/, "legacy contact import route should redirect to the central import area");
  const importPage = readFileSync(new URL("../app/(app)/import/page.tsx", import.meta.url), "utf8");
  assert.match(importPage, /FullContactImport/);
  assert.match(importPage, /ContactImport/);
  assert.match(importPage, /role="customer"/);
  assert.match(importPage, /role="supplier"/);
  assert.doesNotMatch(readFileSync(new URL("../app/(app)/sales/customers/page.tsx", import.meta.url), "utf8"), /ContactImport/);
  assert.match(importPage, /MigrationWorkspace/);
  assert.match(readFileSync(new URL("../app/(app)/migrate/page.tsx", import.meta.url), "utf8"), /redirect\("\/import"\)/);
  assert.ok(!NAV.flatMap((g) => g.items).some((i) => i.href === "/migrate"));
  assert.equal(NAV.flatMap((g) => g.items).filter((i) => i.href === "/sales/customers").length, 1);
  assert.equal(NAV.find((g) => g.label === "Contacts")?.items.filter((i) => i.href === "/sales/customers").length, 1);
  assert.equal(QUICK_CREATE.find((i) => i.label === "Contacts page")?.href, "/contacts");
  assert.ok(!SALES_TABS.some((i) => i.href === "/sales/customers"));
  const hrefsByLabel = new Map();
  for (const { g: group, i: item } of [...NAV.flatMap((g) => g.items.map((i) => ({ g: g.label, i }))), ...NAV_LINKS.map((i) => ({ g: i.label, i }))]) {
    if (!item.available) continue;
    const previous = hrefsByLabel.get(item.href);
    assert.equal(previous, undefined, `duplicate destination ${item.href}: ${previous} and ${group.label}`);
    hrefsByLabel.set(item.href, group.label);
  }
  assert.ok(!NAV.some((g) => g.label === "More"));
  assert.equal(new Set([...NAV.map((g) => g.label), ...NAV_LINKS.map((i) => i.label)]).size, NAV.length + NAV_LINKS.length, "top-level destinations must have unique labels");
  assert.ok(NAV_LINKS.every((i) => i.available && i.href), "standalone top-level links must resolve directly, not open a one-item submenu");
  const labels = [...NAV.map((g) => g.label), ...NAV.flatMap((g) => g.items.map((i) => i.label))];
  for (const l of labels) assert.ok(!/xero/i.test(l) && !/do more with/i.test(l), l);
  assert.ok(!NAV.some((g) => g.items.some((i) => i.href === "/banking" && i.label === "Bank accounts")));
});
test("destinations belong to clear app areas and single-destination groups open directly", () => {
  const owner = (href) => NAV.find((g) => g.items.some((i) => i.href === href))?.label;
  for (const [href, group] of [
    ["/overview", "Home"], ["/sales/invoices", "Sales"], ["/sales/customers", "Contacts"],
    ["/purchases", "Purchases"], ["/purchases/suppliers", "Contacts"], ["/reports", "Reporting"],
    ["/accounting?tab=accounts", "Accounting"], ["/tax/gst", "Tax"], ["/contacts", "Contacts"],
    ["/items", "Items"], ["/billing/catalog", "Billing"], ["/crm/deals", "CRM"],
    ["/time/projects", "Projects"], ["/documents", "Organisation"],
    ["/settings/branding", "Organisation"], ["/settings/conversion-balances", "Organisation"],
    ["/time/staff-rates", "Projects"],
  ]) assert.equal(owner(href), group, `${href} belongs in ${group}`);
  assert.equal(NAV_LINKS.length, 1);
  assert.equal(NAV_LINKS[0].label, "Import");
  assert.equal(NAV_LINKS[0].href, "/import");
  assert.equal(owner("/import"), undefined, "Import is standalone, not presented as a one-item feature group");
  assert.equal(owner("/documents"), "Organisation");
  assert.equal(NAV.find((g) => g.label === "Organisation")?.items.map((i) => i.label).join(", "), "Company documents, Opening balances, Branding themes");
  assert.deepEqual(NAV.find((g) => g.label === "Billing")?.items.map((i) => i.label), ["Subscriptions", "Plans, addons and coupons", "Pricing widget setup", "Subscription events"]);
  assert.ok(!NAV.find((g) => g.label === "Projects")?.items.some((i) => ["/settings/branding", "/settings/conversion-balances"].includes(i.href)));
  assert.ok(!NAV.find((g) => g.label === "Accounting")?.items.some((i) => i.href === "/documents"));
  const appNav = readFileSync(new URL("../components/AppNav.tsx", import.meta.url), "utf8");
  assert.match(appNav, /<nav aria-label="Main"/);
  assert.match(appNav, /group\.items\.length === 1 && group\.items\[0\]\.available/);
  assert.match(appNav, /<Link className="topnav-toggle" href=\{group\.items\[0\]\.href\}/);
});
test("every item states availability; unavailable items never carry a link target to a 404", () => {
  for (const g of NAV) for (const i of g.items) {
    assert.equal(typeof i.available, "boolean", i.label);
    if (i.note) assert.equal(i.available, false, i.label);
  }
});
test("quick create labels destinations honestly and does not advertise unimplemented shortcuts", () => {
  assert.deepEqual(QUICK_CREATE.map((q) => q.label), ["Invoice", "Bills page", "Contacts page", "Quotes page", "Purchase orders page", "Documents page", "Manual journals page", "Spend money", "Receive money", "Transfer money"]);
  assert.ok(QUICK_CREATE.every((q) => !("shortcut" in q)));
});

test("every available link and quick-create target resolves to an existing page", () => {
  const pages = (p) => ["(app)", ""].some((g) => existsSync(new URL(`../app/${g ? g + "/" : ""}${p}/page.tsx`, import.meta.url)));
  const hrefs = [...NAV.flatMap((g) => g.items), ...NAV_LINKS, ...QUICK_CREATE, ...ACCOUNT_LINKS].filter((i) => i.available).map((i) => i.href.split(/[?#]/)[0].replace(/^\//, ""));
  for (const h of hrefs) assert.ok(pages(h), `no page for /${h}`);
});

 test("personal pages use supported account features and menu remains keyboard accessible", () => {
  assert.ok(ACCOUNT_LINKS.find((i) => i.label === "Profile")?.available);
  assert.ok(ACCOUNT_LINKS.find((i) => i.label === "Account")?.available);
  assert.deepEqual(ACCOUNT_LINKS.find((i) => i.label === "Company invitations"), { label: "Company invitations", href: "/invitations", available: true });
  const profile = readFileSync(new URL("../app/(app)/settings/profile/page.tsx", import.meta.url), "utf8");
  const account = readFileSync(new URL("../app/(app)/settings/account/page.tsx", import.meta.url), "utf8");
  const nav = readFileSync(new URL("../components/AppNav.tsx", import.meta.url), "utf8");
  assert.match(profile, /\/v1\/me/);
  assert.match(profile, /Email changes are not supported/);
  assert.match(account, /href="\/forgot"/);
  assert.match(account, /\/api\/v1\/auth\/logout/);
  assert.match(nav, /aria-expanded/);
  assert.match(nav, /e.key === "Escape"/);
  assert.match(nav, /\.focus\(\)/);
});
