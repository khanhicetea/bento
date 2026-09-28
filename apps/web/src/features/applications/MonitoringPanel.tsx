import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { formatBytes, formatRelative } from "../../lib/format.ts";
import { Cell, DomainError, DomainLoading } from "../../components/DomainState.tsx";

// Polls only while the page is visible (React Query pauses background refetch).
const pollMs = 10_000;

export function MonitoringPanel({ app }: { app: T.App }) {
  const query = useQuery({
    queryKey: keys.apps.metrics(app.id),
    queryFn: ({ signal }) => api.apps.metrics(app.id, signal),
    refetchInterval: pollMs,
    refetchIntervalInBackground: false,
  });
  if (query.isPending) return <DomainLoading label="metrics" />;
  if (query.isError) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const m = query.data;
  if (!m.running) {
    return (
      <div className="box">
        <Cell>
          <p className="note">No running instance. Metrics appear once the app is started.</p>
        </Cell>
      </div>
    );
  }
  const memPct = m.memoryLimit > 0 ? (m.memoryBytes / m.memoryLimit) * 100 : 0;
  const cpuLimit = app.resources.cpuMillis / 1000;
  return (
    <>
      <div className="box box--4">
        <Metric value={`${m.cpuPercent.toFixed(1)}%`} label={`CPU · limit ${cpuLimit} cores`} />
        <Metric
          value={formatBytes(m.memoryBytes)}
          label={m.memoryLimit > 0 ? `Memory · ${memPct.toFixed(0)}% of ${formatBytes(m.memoryLimit)}` : "Memory"}
        />
        <Metric value={`${m.pids}`} label={`Processes · limit ${app.resources.pids}`} />
        <Metric value={`${formatBytes(m.networkRx)} / ${formatBytes(m.networkTx)}`} label="Network in / out" />
      </div>
      <div className="box">
        <Cell
          title={`Processes (${m.processes.length} of ${m.processTotal})`}
          action={
            <span className="note" title={m.sampledAt}>
              Updated {formatRelative(m.sampledAt)} · disk {formatBytes(m.blockRead)} read / {formatBytes(m.blockWrite)}{" "}
              written
            </span>
          }
        >
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-muted-foreground">
                <tr>
                  <th className="py-1 pr-3 font-medium">PID</th>
                  <th className="py-1 pr-3 font-medium">User</th>
                  <th className="py-1 pr-3 text-right font-medium">CPU %</th>
                  <th className="py-1 pr-3 text-right font-medium">Mem %</th>
                  <th className="py-1 pr-3 text-right font-medium">RSS</th>
                  <th className="py-1 pr-3 font-medium">Uptime</th>
                  <th className="py-1 font-medium">Command</th>
                </tr>
              </thead>
              <tbody className="font-mono">
                {processTree(m.processes).map(({ p, prefix }) => (
                  <tr key={p.pid} className="border-t">
                    <td className="py-1 pr-3">{p.pid}</td>
                    <td className="py-1 pr-3">{p.user}</td>
                    <td className="py-1 pr-3 text-right">{p.cpuPercent.toFixed(1)}</td>
                    <td className="py-1 pr-3 text-right">{p.memPercent.toFixed(1)}</td>
                    <td className="py-1 pr-3 text-right">{formatBytes(p.rssBytes)}</td>
                    <td className="py-1 pr-3">{p.elapsed}</td>
                    <td className="max-w-md truncate py-1 whitespace-pre" title={p.command}>
                      <span className="text-muted-foreground">{prefix}</span>
                      {p.command}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Cell>
      </div>
    </>
  );
}

function Metric({ value, label }: { value: string; label: string }) {
  return (
    <Cell>
      <div className="metric">
        <strong>{value}</strong>
        <span>{label}</span>
      </div>
    </Cell>
  );
}

const byUsage = (a: T.AppProcess, b: T.AppProcess) => b.cpuPercent - a.cpuPercent || b.rssBytes - a.rssBytes;

/** Flattens processes into parent-before-child order with ASCII tree prefixes; siblings sort by CPU, then RSS. */
function processTree(processes: T.AppProcess[]): Array<{ p: T.AppProcess; prefix: string }> {
  const pids = new Set(processes.map((p) => p.pid));
  const children = new Map<string, T.AppProcess[]>();
  const roots: T.AppProcess[] = [];
  for (const p of processes) {
    if (p.ppid !== p.pid && pids.has(p.ppid)) children.set(p.ppid, [...(children.get(p.ppid) ?? []), p]);
    else roots.push(p);
  }
  const out: Array<{ p: T.AppProcess; prefix: string }> = [];
  const seen = new Set<string>();
  const walk = (nodes: T.AppProcess[], indent: string, root: boolean) => {
    nodes.sort(byUsage).forEach((p, i) => {
      if (seen.has(p.pid)) return;
      seen.add(p.pid);
      const last = i === nodes.length - 1;
      out.push({ p, prefix: root ? "" : `${indent}${last ? "└─ " : "├─ "}` });
      walk(children.get(p.pid) ?? [], root ? "" : `${indent}${last ? "   " : "│  "}`, false);
    });
  };
  walk(roots, "", true);
  return out;
}
