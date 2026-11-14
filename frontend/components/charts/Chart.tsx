"use client";
import { useId } from "react";
import { formatCents } from "@/lib/money";
import { axisTicks, compactMoney, monthLabel, niceMax, type ChartPoint } from "./model";

// One hand-built chart for the dashboard, snapshot and report screens. Inline SVG only: no chart
// dependency, no animation (so prefers-reduced-motion needs no special case). The same numbers are
// always repeated in a visually hidden table for screen readers and for copying.
const WIDTH = 720;
const HEIGHT = 280;
const PAD = { top: 18, right: 14, bottom: 38, left: 70 };

export default function Chart({
  series,
  title,
  description,
  kind = "bar",
  empty = "No posted revenue or spending in this period.",
}: {
  series: ChartPoint[];
  title: string;
  description?: string;
  kind?: "bar" | "line";
  empty?: string;
}) {
  const id = useId();
  const points = series;
  const four = points.some(p => p.cost != null || p.gross != null);
  const peak = Math.max(0, ...points.flatMap(p => [p.revenue, p.expense, p.cost ?? 0, p.gross ?? 0]));
  if (!points.length || peak === 0) {
    return <p className="chart-empty" role="status">{empty}</p>;
  }

  const max = niceMax(peak);
  const ticks = axisTicks(max);
  const plotWidth = WIDTH - PAD.left - PAD.right;
  const plotHeight = HEIGHT - PAD.top - PAD.bottom;
  const step = plotWidth / points.length;
  const x = (i: number) => PAD.left + step * (i + 0.5);
  const y = (value: number) => PAD.top + plotHeight - (Math.max(0, value) / max) * plotHeight;
  const baseline = PAD.top + plotHeight;
  const barWidth = Math.min(18, Math.max(3, step * 0.28));
  const path = (key: "revenue" | "expense") =>
    points.map((p, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(p[key]).toFixed(1)}`).join(" ");
  const subtitle = description ?? `Monthly revenue and spending from ${monthLabel(points[0].label)} to ${monthLabel(points[points.length - 1].label)}.`;

  return (
    <figure className="chart">
      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="img" aria-labelledby={`${id}-title ${id}-desc`}>
        <title id={`${id}-title`}>{title}</title>
        <desc id={`${id}-desc`}>{subtitle} Values in Singapore dollars.</desc>
        {ticks.map(tick => (
          <g key={tick}>
            <line className="chart-gridline" x1={PAD.left} x2={WIDTH - PAD.right} y1={y(tick)} y2={y(tick)} />
            <text className="chart-axis-label" x={PAD.left - 8} y={y(tick) + 4} textAnchor="end">{compactMoney(tick)}</text>
          </g>
        ))}
        <line className="chart-baseline" x1={PAD.left} x2={WIDTH - PAD.right} y1={baseline} y2={baseline} />
        {points.map((point, i) => (
          <text key={point.label} className="chart-axis-label" x={x(i)} y={HEIGHT - PAD.bottom + 18} textAnchor="middle">
            {monthLabel(point.label)}
          </text>
        ))}
        {kind === "bar"
          ? points.map((point, i) => (
              four ? <g key={point.label}>
                <rect className="chart-revenue" x={x(i) - barWidth * 1.6 - 3} y={y(point.revenue)} width={barWidth * 0.8} height={baseline - y(point.revenue)} rx={2} />
                <rect className="chart-spending" x={x(i) - barWidth * 0.6} y={y(point.cost ?? 0)} width={barWidth * 0.8} height={baseline - y(point.cost ?? 0)} rx={2} />
                <rect className="chart-cost" x={x(i) + barWidth * 0.4} y={y(point.expense)} width={barWidth * 0.8} height={baseline - y(point.expense)} rx={2} />
                <rect className="chart-profit" x={x(i) + barWidth * 1.4} y={y(point.gross ?? 0)} width={barWidth * 0.8} height={baseline - y(point.gross ?? 0)} rx={2} />
              </g> : <g key={point.label}>
                <rect className="chart-revenue" x={x(i) - barWidth - 2} y={y(point.revenue)} width={barWidth} height={baseline - y(point.revenue)} rx={3} />
                <rect className="chart-spending" x={x(i) + 2} y={y(point.expense)} width={barWidth} height={baseline - y(point.expense)} rx={3} />
              </g>
            ))
          : (
            <g fill="none" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round">
              <path className="chart-revenue-line" d={path("revenue")} />
              <path className="chart-spending-line" d={path("expense")} />
              {points.map((point, i) => (
                <g key={point.label}>
                  <circle className="chart-revenue" cx={x(i)} cy={y(point.revenue)} r={2.5} />
                  <circle className="chart-spending" cx={x(i)} cy={y(point.expense)} r={2.5} />
                </g>
              ))}
            </g>
          )}
      </svg>
      <figcaption className="chart-legend">
        <span><span className="swatch swatch-revenue" aria-hidden="true" />Revenue</span>
        {four ? <><span><span className="swatch swatch-cost" aria-hidden="true" />Cost of sales</span><span><span className="swatch swatch-spending" aria-hidden="true" />Expenses</span><span><span className="swatch swatch-profit" aria-hidden="true" />Gross profit</span></> : <span><span className="swatch swatch-spending" aria-hidden="true" />Spending</span>}
        {description && <span className="chart-note">{description}</span>}
      </figcaption>
      <table className="sr-only">
        <caption>{title}</caption>
        <thead><tr><th scope="col">Month</th><th scope="col">Revenue</th>{four && <th scope="col">Cost of sales</th>}<th scope="col">Expenses</th>{four && <th scope="col">Gross profit</th>}<th scope="col">Net</th></tr></thead>
        <tbody>
          {points.map(point => (
            <tr key={point.label}>
              <th scope="row">{point.label}</th>
              <td>{formatCents(point.revenue)}</td>
              {four && <td>{formatCents(point.cost ?? 0)}</td>}
              <td>{formatCents(point.expense)}</td>
              {four && <td>{formatCents(point.gross ?? 0)}</td>}
              <td>{formatCents(point.revenue - point.expense)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}
