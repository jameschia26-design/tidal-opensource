// Dashboard pieces: chart axes and money labels are pure helpers; the chart component must render
// real axes, a screen-reader table and a clear empty state; KPI tiles carry a period note.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { axisTicks, compactMoney, monthLabel, niceMax, periodLabel } from "../components/charts/model.ts";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");

test("chart axes round up to readable money ticks", () => {
  assert.equal(niceMax(0), 1);
  assert.equal(niceMax(1234500), 2000000);
  assert.deepEqual(axisTicks(2000000), [0, 500000, 1000000, 1500000, 2000000]);
  assert.deepEqual(axisTicks(1), [0, 0.25, 0.5, 0.75, 1]);
});

test("chart money stays compact, full cents live in tables", () => {
  assert.equal(compactMoney(0), "S$0");
  assert.equal(compactMoney(50000), "S$500");
  assert.equal(compactMoney(125000), "S$1.25k");
  assert.equal(compactMoney(-125000), "-S$1.25k");
  assert.equal(compactMoney(123456789), "S$1.23m");
});

test("month and period labels are readable and never invent a future end", () => {
  assert.equal(monthLabel("2026-01"), "Jan");
  assert.equal(monthLabel("not-a-month"), "not-a-month");
  assert.equal(periodLabel("2026-01-01", "2026-11-03"), "1 Jan – 3 Nov 2026");
  assert.equal(periodLabel("2025-07-01", "2026-03-31"), "1 Jul 2025 – 31 Mar 2026");
  assert.equal(periodLabel("2026-01-01", "today"), "2026-01-01 to today");
});

test("one chart component draws axes, a legend and an accessible table, with an empty state", () => {
  const chart = read("components/charts/Chart.tsx");
  assert.match(chart, /<svg[^>]+role="img"[^>]+aria-labelledby/);
  assert.match(chart, /<title id=/);
  assert.match(chart, /<desc id=/);
  assert.match(chart, /className="chart-axis-label"/);
  assert.match(chart, /className="chart-gridline"/);
  assert.match(chart, /className="chart-legend"/);
  assert.match(chart, /className="sr-only"/);
  assert.match(chart, /<th scope="col">Month<\/th>/);
  assert.match(chart, /formatCents\(point\.revenue\)/);
  assert.match(chart, /className="chart-empty" role="status"/);
});

test("dashboard, snapshot, analytics and visualization pages share that chart and KPI tiles", () => {
  const overview = read("app/(app)/overview/page.tsx");
  const widgets = read("components/HomeWidgets.tsx");
  const snapshot = read("app/(app)/accounting/snapshot/page.tsx");
  const analytics = read("app/(app)/reports/analytics/page.tsx");
  const dashboards = read("app/(app)/reports/dashboards/page.tsx");
  const visualizations = read("app/(app)/reports/visualizations/page.tsx");
  assert.match(overview, /title="Dashboard"/);
  assert.match(widgets, /className="kpi-grid"/);
  assert.match(widgets, /<Chart/);
  assert.match(widgets, /tidal\.home\.widgets/);
  assert.match(widgets, /periodLabel\(period\.from, period\.to\)/);
  assert.match(snapshot, /<Kpi /);
  assert.match(snapshot, /<Chart/);
  assert.match(analytics, /<Chart /);
  assert.match(dashboards, /<Chart /);
  assert.match(visualizations, /kind="line"/);
  assert.doesNotMatch(analytics + visualizations, /className="bar-chart"/);
  assert.match(read("components/Kpi.tsx"), /className="kpi-note"/);
});
