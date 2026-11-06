import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BarChart3 } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { formatBytes, formatRelative } from "../../lib/format.ts";
import { Cell, DomainError, DomainLoading, EmptyState } from "../../components/DomainState.tsx";

const compactFormat = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
function compact(n: number) {
  return compactFormat.format(n);
}

/** Traffic tab: hourly GoAccess reports of the hosts whose access log is on. */
export function TrafficPanel({ host, onHost }: { host: string | null; onHost: (host: string) => void }) {
  const hosts = useQuery({ queryKey: keys.hosts, queryFn: ({ signal }) => api.hosts.list(signal) });
  if (hosts.isPending) return <DomainLoading label="hosts" />;
  if (hosts.error) return <DomainError message={messageOf(hosts.error)} onRetry={() => void hosts.refetch()} />;
  const logged = hosts.data.hosts.filter((h) => h.route.accessLog);
  const first = logged[0];
  if (!first) {
    return (
      <div className="box">
        <div className="cell">
          <EmptyState title="No access logs" body="Turn on Access log" />
        </div>
      </div>
    );
  }
  const selected = host && logged.some((h) => h.name === host) ? host : first.name;
  return (
    <>
      <div className="toolbar">
        <div className="seg" role="group" aria-label="Host">
          {logged.map((h) => (
            <button
              key={h.name}
              type="button"
              className="seg__data"
              aria-pressed={h.name === selected}
              onClick={() => onHost(h.name)}
            >
              {h.name}
            </button>
          ))}
        </div>
      </div>
      <HostTraffic key={selected} host={selected} />
    </>
  );
}

function HostTraffic({ host }: { host: string }) {
  const query = useQuery({
    queryKey: keys.hostTraffic(host),
    queryFn: ({ signal }) => api.hosts.traffic(host, signal),
    refetchInterval: 5 * 60_000,
  });
  if (query.isPending) return <DomainLoading label="traffic" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const t = query.data;
  if (t.state !== "ready") {
    return (
      <div className="box">
        {t.lastError && <RunFailed traffic={t} />}
        <div className={t.state === "waiting" ? "cell cell--caution" : "cell"}>
          {t.state === "waiting" ? (
            <EmptyState mood="busy" title="First report" body={`Ready within ${t.intervalMinutes} min`} />
          ) : (
            <EmptyState title="Access log off" body="Turn on Access log" />
          )}
        </div>
      </div>
    );
  }
  const serverErrors = t.statuses.find((s) => s.label === "5xx")?.requests ?? 0;
  const errorShare = t.requests > 0 ? (serverErrors / t.requests) * 100 : 0;
  const slowest = [...t.paths].sort((a, b) => b.avgMs - a.avgMs).slice(0, 8);
  return (
    <>
      <div className="box box--4">
        {t.lastError && <RunFailed traffic={t} />}
        <Metric label="Requests" value={compact(t.requests)} hint={`${t.from} → ${t.to}`} />
        <Metric label="Visitors" value={compact(t.visitors)} hint="unique IP · day · agent" />
        <Metric label="Data sent" value={formatBytes(t.bytes)} hint={`${t.keepDays} days kept`} />
        <Metric
          label="Server errors"
          value={`${errorShare.toFixed(errorShare < 10 ? 1 : 0)} %`}
          hint={`${compact(serverErrors)} × 5xx`}
          caution={errorShare >= 1}
        />
        <Cell
          title="Daily requests"
          icon={<BarChart3 />}
          className="cell--wide"
          action={
            <span className="label text-xs text-muted-foreground">
              Updated {formatRelative(t.generatedAt)}
              {t.pendingBytes > 0 && ` · ${formatBytes(t.pendingBytes)} pending`}
            </span>
          }
        >
          <DayBars days={fillDays(t.days, t.to, t.keepDays)} />
        </Cell>
      </div>
      <div className="box box--2">
        <Cell title="Top paths">
          <RankList
            rows={t.paths.slice(0, 10).map((p) => [`${p.method} ${p.path}`, `${compact(p.requests)} req`])}
            empty="No requests"
          />
        </Cell>
        <Cell title="Slowest paths">
          <RankList
            rows={slowest.map((p) => [`${p.method} ${p.path}`, `${formatMs(p.avgMs)} avg · ${formatMs(p.maxMs)} max`])}
            empty="No requests"
          />
        </Cell>
      </div>
      <div className="box box--4">
        <Cell title="Status codes">
          <RankList rows={t.codes.slice(0, 8).map((c) => [c.label, share(c.requests, t.requests)])} empty="None" />
        </Cell>
        <Cell title="Top clients">
          <RankList rows={t.clients.slice(0, 8).map((c) => [c.label, `${compact(c.requests)} req`])} empty="None" />
        </Cell>
        <Cell title="Agents">
          <RankList rows={t.agents.slice(0, 8).map((c) => [c.label, share(c.requests, t.requests)])} empty="None" />
        </Cell>
        <Cell title="Referrers">
          <RankList rows={t.referrers.slice(0, 8).map((c) => [c.label, `${compact(c.requests)} req`])} empty="None" />
        </Cell>
      </div>
    </>
  );
}

function RunFailed({ traffic }: { traffic: T.HostTraffic }) {
  return (
    <Cell title="Run failed" kind="ume" className="cell--wide">
      <p className="text-sm">
        ✕ {traffic.lastError} · {formatRelative(traffic.lastRunAt)} · retry in ≤ {traffic.intervalMinutes} min
      </p>
    </Cell>
  );
}

function Metric({ label, value, hint, caution }: { label: string; value: string; hint: string; caution?: boolean }) {
  return (
    <Cell kind={caution ? "tamago" : "gohan"} className="flex flex-col gap-2">
      <span className="label text-[0.6875rem] text-muted-foreground">{label}</span>
      <span className="truncate font-mono text-[2.25rem] leading-none font-semibold tracking-tight tabular-nums">
        {caution && <span aria-label="Caution">▲ </span>}
        {value}
      </span>
      <span className="truncate font-mono text-xs text-muted-foreground">{hint}</span>
    </Cell>
  );
}

/** LABEL ······ value rows; long labels truncate with the full text in the tooltip. */
function RankList({ rows, empty }: { rows: Array<[string, string]>; empty: string }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">{empty}</p>;
  return (
    <dl className="kv kv--rank">
      {rows.map(([label, value], i) => (
        <div key={`${label}-${i}`}>
          <dt title={label}>
            <span>{label}</span>
          </dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function share(n: number, total: number) {
  const pct = total > 0 ? (n / total) * 100 : 0;
  return `${compact(n)} · ${pct.toFixed(pct < 10 ? 1 : 0)} %`;
}

function formatMs(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(ms >= 10_000 ? 0 : 1)} s` : `${Math.round(ms)} ms`;
}

/** One slot per day of the kept window ending at `to`; days without data are zero. */
function fillDays(days: T.TrafficCount[], to: string | undefined, keep: number): T.TrafficCount[] {
  if (!to) return days;
  const byDate = new Map(days.map((d) => [d.label, d]));
  const end = Date.parse(`${to}T00:00:00Z`);
  const out: T.TrafficCount[] = [];
  for (let i = keep - 1; i >= 0; i--) {
    const label = new Date(end - i * 86_400_000).toISOString().slice(0, 10);
    out.push(byDate.get(label) ?? { label, requests: 0, visitors: 0, bytes: 0 });
  }
  return out;
}

/** Requests per day as baseline-anchored bars with a per-bar tooltip, plus a table for screen readers. */
function DayBars({ days }: { days: T.TrafficCount[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1, ...days.map((d) => d.requests));
  const active = hover !== null ? days[hover] : undefined;
  return (
    <div className="day-bars">
      <span className="day-bars__max font-mono">{compact(max)}</span>
      <div className="day-bars__plot" aria-hidden="true" onPointerLeave={() => setHover(null)}>
        {days.map((d, i) => (
          <div
            key={d.label}
            className="day-bars__slot"
            data-active={hover === i || undefined}
            onPointerEnter={() => setHover(i)}
          >
            {d.requests > 0 && <span className="day-bars__bar" style={{ height: `${(d.requests / max) * 100}%` }} />}
          </div>
        ))}
        {active && hover !== null && (
          <span
            className="day-bars__tip font-mono"
            style={{ left: `${((hover + 0.5) / days.length) * 100}%` }}
            data-side={hover > days.length * 0.7 ? "left" : "right"}
          >
            {active.label} · {compact(active.requests)} req · {compact(active.visitors)} visitors
          </span>
        )}
      </div>
      <div className="day-bars__axis font-mono" aria-hidden="true">
        <span>{days[0]?.label}</span>
        <span>{days.at(-1)?.label}</span>
      </div>
      <table className="sr-only">
        <caption>Requests per day</caption>
        <thead>
          <tr>
            <th scope="col">Day</th>
            <th scope="col">Requests</th>
            <th scope="col">Visitors</th>
          </tr>
        </thead>
        <tbody>
          {days.map((d) => (
            <tr key={d.label}>
              <th scope="row">{d.label}</th>
              <td>{d.requests}</td>
              <td>{d.visitors}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
