import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, BarChart3, ExternalLink, Lock, Network, Pencil, Plus, Trash2, Waypoints } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { formatRelative } from "../../lib/format.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  Cell,
  CopyableCode,
  DomainError,
  DomainLoading,
  Field,
  EmptyState,
  KeyValues,
  moodOf,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { Mascot } from "../../components/Mascot.tsx";
import { Link } from "wouter";
import { useOperationMutation } from "../applications/useApplications.ts";
import { HostDialogBody, targetLabels, tlsLabels } from "./HostDialog.tsx";
import { TrafficPanel } from "./TrafficPanel.tsx";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

type Tab = "edge" | "traffic" | "tunnel" | "utils";
const tabLabels: Record<Tab, string> = { edge: "Edge", traffic: "Traffic", tunnel: "Tunnel", utils: "Utils" };

export function RoutingPage() {
  const [tab, setTab] = useState<Tab>("edge");
  const [trafficHost, setTrafficHost] = useState<string | null>(null);
  const showTraffic = (host: string) => {
    setTrafficHost(host);
    setTab("traffic");
  };
  return (
    <>
      <PageHeader title="Ingress" />
      <div className="seg mb-5" role="tablist" aria-label="Ingress sections">
        {(["edge", "traffic", "tunnel", "utils"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "edge" && <EdgePanel onTraffic={showTraffic} />}
      {tab === "traffic" && <TrafficPanel host={trafficHost} onHost={setTrafficHost} />}
      {tab === "tunnel" && <TunnelPanel />}
      {tab === "utils" && <UtilsPanel />}
    </>
  );
}

function EdgePanel({ onTraffic }: { onTraffic: (host: string) => void }) {
  const query = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  if (query.isPending) return <DomainLoading label="edge" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return (
    <>
      <EdgeForm status={query.data} />
      <HostsPanel onTraffic={onTraffic} />
    </>
  );
}

function EdgeMetricsCell() {
  const query = useQuery({
    queryKey: keys.edgeMetrics,
    queryFn: async ({ signal }) => {
      const m = await api.edge.metrics(signal);
      recordRate(m.requestsPerSecond);
      return m;
    },
    refetchInterval: 5_000,
  });
  const m = query.data;
  return (
    <>
      <Cell
        title="Traffic"
        icon={<Activity />}
        className="edge-metrics flex flex-col"
        action={
          m && (
            <span className="label flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="dot dot--wait" aria-hidden="true" />
              Live · 5 s
            </span>
          )
        }
      >
        {query.error ? (
          <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
        ) : !m ? (
          <DomainLoading label="edge metrics" />
        ) : (
          <div className="grid flex-1 grid-rows-[minmax(0,1fr)_auto] gap-2">
            <RateChart total={m.requests} />
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
              <MetricCard label="Connections" value={m.active} hint={`${m.acceptsPerSecond.toFixed(1)} new / s`} />
              <MetricCard
                label="Connection states"
                value={`${m.reading}·${m.writing}·${m.waiting}`}
                hint="reading · writing · idle"
              >
                <StateBar reading={m.reading} writing={m.writing} waiting={m.waiting} />
              </MetricCard>
              <MetricCard
                label="Dropped"
                value={compact(m.dropped)}
                hint={m.dropped > 0 ? "worker_connections limit hit" : `${compact(m.handled)} handled`}
                tone={m.dropped > 0 ? "bad" : "good"}
              />
            </div>
          </div>
        )}
      </Cell>
    </>
  );
}

type RateSample = { at: number; rate: number };

// Requests/s history is kept client-side only: it starts empty on page load and
// keeps the last 5 minutes of 5 s polls across tab switches.
const rateHistory: RateSample[] = [];
const rateHistoryMax = 60;
function recordRate(rate: number) {
  rateHistory.push({ at: Date.now(), rate });
  if (rateHistory.length > rateHistoryMax) rateHistory.shift();
}

const timeFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });

/** Live line of requests per second, with a crosshair tooltip on hover. */
function RateChart({ total }: { total: number }) {
  const [hover, setHover] = useState<number | null>(null);
  const samples = rateHistory;
  const latest = samples.at(-1)?.rate ?? 0;
  const max = Math.max(1, ...samples.map((s) => s.rate)) * 1.15;
  const w = 100;
  const h = 100;
  // Fixed x slots so the line grows in from the right as samples arrive.
  const x = (i: number) => w - ((samples.length - 1 - i) / (rateHistoryMax - 1)) * w;
  const y = (rate: number) => h - (rate / max) * h;
  const points = samples.map((s, i) => `${x(i).toFixed(2)},${y(s.rate).toFixed(2)}`).join(" ");
  const area = samples.length > 1 ? `${x(0)},${h} ${points} ${x(samples.length - 1)},${h}` : "";
  const active = hover !== null ? samples[hover] : undefined;

  const onMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (samples.length === 0) return;
    const rect = event.currentTarget.getBoundingClientRect();
    const pos = ((event.clientX - rect.left) / rect.width) * w;
    const i = Math.round(samples.length - 1 - ((w - pos) / w) * (rateHistoryMax - 1));
    setHover(i < 0 ? null : Math.min(i, samples.length - 1));
  };

  return (
    <div className="flex min-h-[11rem] min-w-0 flex-col gap-2 rounded-[0.875rem] bg-background p-4">
      <div className="flex items-baseline justify-between gap-3">
        <span className="label text-[0.6875rem] text-muted-foreground">Requests / s</span>
        <span className="font-mono text-xs text-muted-foreground">{compact(total)} total</span>
      </div>
      <span className="font-mono text-[2.25rem] leading-none font-semibold tracking-tight tabular-nums">
        {(active?.rate ?? latest).toFixed(1)}
      </span>
      <div
        className="rate-chart"
        role="img"
        aria-label={`Requests per second over the last ${samples.length} samples, now ${latest.toFixed(1)}`}
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
      >
        <span className="rate-chart__max font-mono">{(max / 1.15).toFixed(1)}</span>
        <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden="true">
          <line className="rate-chart__grid" x1="0" x2={w} y1={h / 2} y2={h / 2} />
          <line className="rate-chart__base" x1="0" x2={w} y1={h} y2={h} />
          {area && <polygon className="rate-chart__area" points={area} />}
          {samples.length > 1 && <polyline className="rate-chart__line" points={points} />}
          {active && hover !== null && <line className="rate-chart__cross" x1={x(hover)} x2={x(hover)} y1="0" y2={h} />}
        </svg>
        {samples.length > 0 && (
          <span
            className="rate-chart__dot"
            style={{
              left: `${x(hover ?? samples.length - 1)}%`,
              top: `${y(active?.rate ?? latest)}%`,
            }}
          />
        )}
        {active && hover !== null && (
          <span
            className="rate-chart__tip font-mono"
            style={{ left: `${x(hover)}%` }}
            data-side={x(hover) > 70 ? "left" : "right"}
          >
            {active.rate.toFixed(1)} / s · {timeFormat.format(active.at)}
          </span>
        )}
        {samples.length < 2 && <span className="rate-chart__empty label">Collecting samples</span>}
      </div>
    </div>
  );
}

function MetricCard({
  label,
  value,
  hint,
  tone,
  children,
}: {
  label: string;
  value: React.ReactNode;
  hint: string;
  tone?: "good" | "bad";
  children?: React.ReactNode;
}) {
  const valueColor = tone === "bad" ? "text-destructive" : tone === "good" ? "text-success" : "";
  return (
    <div className="flex min-w-0 flex-col justify-between gap-2 rounded-[0.875rem] bg-background p-4">
      <span className="label text-[0.6875rem] text-muted-foreground">{label}</span>
      <span
        className={`truncate font-mono text-[2.25rem] leading-none font-semibold tracking-tight tabular-nums ${valueColor}`}
      >
        {value}
      </span>
      {children}
      <span className="truncate font-mono text-xs text-muted-foreground">{hint}</span>
    </div>
  );
}

function StateBar({ reading, writing, waiting }: { reading: number; writing: number; waiting: number }) {
  const total = reading + writing + waiting || 1;
  const part = (n: number, color: string) => <span className={color} style={{ width: `${(n / total) * 100}%` }} />;
  return (
    <div className="flex h-1.5 overflow-hidden rounded-full bg-muted">
      {part(reading, "bg-info")}
      {part(writing, "bg-warning")}
      {part(waiting, "bg-muted-foreground/40")}
    </div>
  );
}

const compactFormat = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
function compact(n: number) {
  return compactFormat.format(n);
}

function Switch({ checked, label, onChange }: { checked: boolean; label: string; onChange: (on: boolean) => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className="switch"
      onClick={() => onChange(!checked)}
    >
      <span />
    </button>
  );
}

function EdgeForm({ status }: { status: T.EdgeStatus }) {
  // Saved settings come back canonical (defaults, CIDRs). Each form cell remounts from its own saved
  // fields only, so saving one cell never discards the other cell's draft.
  const { acmeEmail, acmeUrl, ...listeners } = status.settings;
  return (
    <section className="box box--edge" aria-label="Edge">
      <EdgeConfigCell key={JSON.stringify(listeners)} status={status} />
      <AcmeCell key={JSON.stringify({ acmeEmail, acmeUrl })} status={status} />
      {status.state === "healthy" ? (
        <EdgeMetricsCell />
      ) : (
        <Cell kind="kara" className="edge-metrics grid place-items-center">
          <EmptyState title="No traffic" body="Edge is not running." />
        </Cell>
      )}
    </section>
  );
}

type EdgeListeners = Omit<T.EdgeSettings, "acmeEmail" | "acmeUrl">;

/** Head actions of an edge form cell: state badge, Reset while dirty, Save. */
function EdgeCellActions({
  badge,
  dirty,
  pending,
  onReset,
}: {
  badge?: React.ReactNode;
  dirty: boolean;
  pending: boolean;
  onReset: () => void;
}) {
  return (
    <span className="flex items-center gap-2">
      {badge}
      {dirty && (
        <Button type="button" size="xs" variant="ghost" disabled={pending} onClick={onReset}>
          Reset
        </Button>
      )}
      <Button type="submit" size="xs" disabled={!dirty || pending}>
        {pending ? "Saving" : "Save"}
      </Button>
    </span>
  );
}

/** Listeners and front proxy: saves only these fields, keeping the saved ACME settings. */
function EdgeConfigCell({ status }: { status: T.EdgeStatus }) {
  const { acmeEmail: _email, acmeUrl: _url, ...saved } = status.settings;
  const [settings, setSettings] = useState<EdgeListeners>(saved);
  const save = useOperationMutation(() => api.edge.set({ ...status.settings, ...settings }));
  const dirty = JSON.stringify(settings) !== JSON.stringify(saved);
  const set = (next: Partial<EdgeListeners>) => setSettings({ ...settings, ...next });
  const setFront = (next: Partial<T.FrontProxySettings>) =>
    setSettings({ ...settings, frontProxy: { ...settings.frontProxy, ...next } });
  const [trusted, setTrusted] = useState(saved.frontProxy.trustedProxies.join(", "));
  const [httpProxies, setHTTPProxies] = useState(saved.httpProxies.join(", "));
  const reset = () => {
    setSettings(saved);
    setTrusted(saved.frontProxy.trustedProxies.join(", "));
    setHTTPProxies(saved.httpProxies.join(", "));
  };
  const front = settings.frontProxy;
  return (
    <Cell
      className="edge-config"
      title="Edge"
      icon={<Network />}
      kind={dirty ? "tamago" : "gohan"}
      action={
        <EdgeCellActions
          badge={<StateBadge state={status.settings.enabled ? status.state : "absent"} />}
          dirty={dirty}
          pending={save.isPending}
          onReset={reset}
        />
      }
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(undefined);
      }}
    >
      <div className="grid gap-3">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          <span className="flex items-center gap-3">
            <Switch checked={settings.enabled} label="Edge enabled" onChange={(enabled) => set({ enabled })} />
            <span className="label text-xs">Enabled</span>
          </span>
          <span className="flex items-center gap-3">
            <Switch checked={settings.http3} label="HTTP/3" onChange={(http3) => set({ http3 })} />
            <span className="label text-xs">HTTP/3 · QUIC</span>
          </span>
        </div>
        <dl className="kv kv--edit">
          <div>
            <dt>Bind</dt>
            <dd>
              <Input
                aria-label="Bind address"
                value={settings.bind}
                spellCheck={false}
                onChange={(event) => set({ bind: event.target.value })}
              />
            </dd>
          </div>
          <div>
            <dt>HTTP</dt>
            <dd>
              <Input
                aria-label="HTTP port"
                type="number"
                value={settings.httpPort}
                onChange={(event) => set({ httpPort: Number(event.target.value) })}
              />
            </dd>
          </div>
          <div>
            <dt>HTTPS</dt>
            <dd>
              <Input
                aria-label="HTTPS port"
                type="number"
                value={settings.httpsPort}
                onChange={(event) => set({ httpsPort: Number(event.target.value) })}
              />
            </dd>
          </div>
        </dl>
        <div className="grid gap-3 border-t border-border pt-3">
          <span className="flex items-center gap-3">
            <Switch checked={front.enabled} label="Front proxy enabled" onChange={(enabled) => setFront({ enabled })} />
            <span className="label text-xs">Front proxy</span>
            <span className="text-xs text-muted-foreground">PROXY protocol</span>
          </span>
          {front.enabled && (
            <>
              <dl className="kv kv--edit">
                <div>
                  <dt>Bind</dt>
                  <dd>
                    <Input
                      aria-label="Front proxy bind address"
                      value={front.bind}
                      spellCheck={false}
                      onChange={(event) => setFront({ bind: event.target.value })}
                    />
                  </dd>
                </div>
                <div>
                  <dt>HTTP</dt>
                  <dd>
                    <Input
                      aria-label="Front proxy HTTP port"
                      type="number"
                      value={front.httpPort}
                      onChange={(event) => setFront({ httpPort: Number(event.target.value) })}
                    />
                  </dd>
                </div>
                <div>
                  <dt>HTTPS</dt>
                  <dd>
                    <Input
                      aria-label="Front proxy HTTPS port"
                      type="number"
                      value={front.httpsPort}
                      onChange={(event) => setFront({ httpsPort: Number(event.target.value) })}
                    />
                  </dd>
                </div>
              </dl>
              <Field label="Trusted proxies" hint="Front proxy IPs or CIDRs, comma separated">
                <Input
                  value={trusted}
                  placeholder="100.64.0.1"
                  spellCheck={false}
                  onChange={(event) => {
                    setTrusted(event.target.value);
                    setFront({
                      trustedProxies: event.target.value
                        .split(/[\s,]+/)
                        .map((value) => value.trim())
                        .filter(Boolean),
                    });
                  }}
                />
              </Field>
            </>
          )}
        </div>
        <div className="grid gap-3 border-t border-border pt-3">
          <Field
            label="HTTP proxies"
            hint="CDN or reverse proxy IPs or CIDRs that end TLS before the edge, comma separated. Only their X-Forwarded-For and X-Forwarded-Proto are believed."
          >
            <Input
              value={httpProxies}
              placeholder="173.245.48.0/20"
              spellCheck={false}
              onChange={(event) => {
                setHTTPProxies(event.target.value);
                set({
                  httpProxies: event.target.value
                    .split(/[\s,]+/)
                    .map((value) => value.trim())
                    .filter(Boolean),
                });
              }}
            />
          </Field>
        </div>
      </div>
      {save.error && <p className="note note--bad mt-3">{messageOf(save.error)}</p>}
    </Cell>
  );
}

/** ACME account: saves only the contact email and directory, keeping the saved listeners. */
function AcmeCell({ status }: { status: T.EdgeStatus }) {
  const saved = { acmeEmail: status.settings.acmeEmail, acmeUrl: status.settings.acmeUrl };
  const [acme, setAcme] = useState(saved);
  const save = useOperationMutation(() => api.edge.set({ ...status.settings, ...acme }));
  const dirty = acme.acmeEmail !== saved.acmeEmail || acme.acmeUrl !== saved.acmeUrl;
  return (
    <Cell
      title="Certificates · ACME"
      icon={<Lock />}
      kind={dirty ? "tamago" : "gohan"}
      className="edge-config"
      action={<EdgeCellActions dirty={dirty} pending={save.isPending} onReset={() => setAcme(saved)} />}
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(undefined);
      }}
    >
      <div className="grid gap-4">
        <Field label="Contact email">
          <Input
            type="email"
            value={acme.acmeEmail}
            placeholder="ops@example.com"
            onChange={(event) => setAcme({ ...acme, acmeEmail: event.target.value })}
          />
        </Field>
        <Field label="Directory URL">
          <Input
            value={acme.acmeUrl}
            spellCheck={false}
            onChange={(event) => setAcme({ ...acme, acmeUrl: event.target.value })}
          />
        </Field>
      </div>
      {save.error && <p className="note note--bad mt-3">{messageOf(save.error)}</p>}
    </Cell>
  );
}

/** One-line description of where a host points. */
function HostTargetText({ host }: { host: T.Host }) {
  const { target } = host;
  return (
    <>
      <span className="label mr-1.5 text-[0.6875rem] text-muted-foreground">{targetLabels[target.kind]}</span>
      {target.kind === "app" ? (
        <Link href={`/apps/${target.app}`}>{target.app}</Link>
      ) : target.kind === "redirect" ? (
        <span className="font-mono">→ {target.redirectTo}</span>
      ) : (
        <span className="font-mono">
          {target.upstreams?.length ?? 0} upstream{target.upstreams?.length === 1 ? "" : "s"}
        </span>
      )}
    </>
  );
}

function UtilsPanel() {
  const query = useQuery({ queryKey: keys.utils, queryFn: ({ signal }) => api.utils.get(signal) });
  if (query.isPending) return <DomainLoading label="utils listener" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return <UtilsForm settings={query.data} />;
}

function UtilsForm({ settings }: { settings: T.UtilsSettings }) {
  const queryClient = useQueryClient();
  const [baseUrl, setBaseUrl] = useState(settings.baseUrl);
  const save = useMutation({
    mutationFn: () => api.utils.set({ baseUrl: baseUrl.trim() }),
    onSuccess: (next) => {
      queryClient.setQueryData(keys.utils, next);
      setBaseUrl(next.baseUrl);
      void queryClient.invalidateQueries({ queryKey: keys.apps.webhooks });
    },
  });
  return (
    <div className="box box--2">
      <Cell title="Utils base URL">
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <Field
            label="Base URL"
            hint="Where your ingress exposes Bento's utils routes (/_bento/webhook/* and /_bento/dbadmin/*). Used for full webhook URLs and the database browser."
          >
            <Input
              value={baseUrl}
              placeholder="https://utils.example.com"
              spellCheck={false}
              onChange={(event) => setBaseUrl(event.target.value)}
            />
          </Field>
          <div>
            <Button type="submit" disabled={baseUrl.trim() === settings.baseUrl || save.isPending}>
              Save
            </Button>
          </div>
          {save.error && <p className="note note--bad">{messageOf(save.error)}</p>}
        </form>
      </Cell>
      <Cell title="Utils listener" className="cell--muted">
        <p className="note mb-3">
          Route <code>/_bento/webhook/*</code> and <code>/_bento/dbadmin/*</code> on that host to one of these. The edge
          forwards <code>/_bento/webhook/*</code> automatically for its domains.
        </p>
        {settings.targets.length ? (
          <KeyValues
            items={settings.targets.map((target): [string, React.ReactNode] => [
              target.includes("127.0.0.1") || target.includes("[::1]") ? "Host proxy" : "Tunnel / containers",
              <CopyableCode value={target} />,
            ])}
          />
        ) : (
          <p className="note">The utils listener is off (bento serve --utils-listen off).</p>
        )}
      </Cell>
      <DBAdminCell />
    </div>
  );
}

function DBAdminCell() {
  const query = useQuery({ queryKey: keys.dbadmin, queryFn: ({ signal }) => api.dbadmin.get(signal) });
  const toggle = useOperationMutation((enabled: boolean) => api.dbadmin.set(enabled));
  const enabled = query.data?.enabled ?? false;
  return (
    <Cell
      title="Database browser"
      className="cell--wide"
      action={query.data && <StateBadge state={enabled ? query.data.state : "absent"} />}
    >
      {query.error ? (
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      ) : (
        <div className="grid gap-3">
          <p className="note">
            One shared Adminer container on the data network. Each MySQL or PostgreSQL binding gets a{" "}
            <strong>Browse</strong> link that opens it on the utils listener with a single-use ticket, signed in as the
            app's own database user. It holds no credentials; Bento injects them per request.
          </p>
          <div>
            <Button
              variant={enabled ? "outline" : "default"}
              disabled={query.isPending || toggle.isPending}
              onClick={() => toggle.mutate(!enabled)}
            >
              {enabled ? "Disable" : "Enable"}
            </Button>
          </div>
          {toggle.error && <p className="note note--bad">{messageOf(toggle.error)}</p>}
        </div>
      )}
    </Cell>
  );
}

function TunnelPanel() {
  const query = useQuery({ queryKey: keys.tunnel, queryFn: ({ signal }) => api.tunnel.get(signal) });
  const [token, setToken] = useState("");
  const [disableOpen, setDisableOpen] = useState(false);
  const mutation = useOperationMutation((value: string) => api.tunnel.setToken(value));
  return (
    <div className="box box--main">
      <Cell
        title="Cloudflare Tunnel"
        action={query.data && <StateBadge state={query.data.enabled ? query.data.state : "absent"} />}
      >
        <div className="grid gap-4">
          {query.data?.note && <p className="note">{query.data.note}</p>}
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-60 flex-1">
              <Field label="Token" hint="Stored privately · never shown again">
                <Input
                  type="password"
                  autoComplete="off"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                />
              </Field>
            </div>
            <Button
              disabled={!token || mutation.isPending}
              onClick={() => mutation.mutate(token, { onSuccess: () => setToken("") })}
            >
              Save
            </Button>
            {query.data?.enabled && (
              <Button variant="ghost" onClick={() => setDisableOpen(true)}>
                Disable
              </Button>
            )}
          </div>
          {mutation.error && <p className="note note--bad">{messageOf(mutation.error)}</p>}
        </div>
      </Cell>
      <Cell kind={query.data?.enabled ? "gohan" : "kara"}>
        <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
          <Mascot mood={query.data?.enabled ? moodOf(query.data.state) : "idle"} size={112} />
          <span className="label text-sm">{query.data?.enabled ? "Tunnel on" : "Tunnel off"}</span>
          <span className="text-sm text-muted-foreground">
            {query.data?.enabled ? "Edge routes via Cloudflare" : "Edge serves on host ports"}
          </span>
        </div>
      </Cell>
      <ConfirmDialog
        open={disableOpen}
        onOpenChange={setDisableOpen}
        title="Disable tunnel?"
        description="Routes served through the tunnel may become unreachable."
        confirmLabel="Disable"
        pending={mutation.isPending}
        error={mutation.error}
        onConfirm={() => mutation.mutate("", { onSuccess: () => setDisableOpen(false) })}
      />
    </div>
  );
}

function HostsPanel({ onTraffic }: { onTraffic: (host: string) => void }) {
  const query = useQuery({
    queryKey: keys.hosts,
    queryFn: ({ signal }) => api.hosts.list(signal),
    // Certificates issue and back off outside tracked operations.
    refetchInterval: (q) =>
      q.state.data?.hosts.some((h) => h.certificate && h.certificate.state !== "issued") ? 15_000 : false,
  });
  // null: dialog closed; "new" | host name: adding or editing.
  const [editing, setEditing] = useState<string | null>(null);
  const [removeTarget, setRemoveTarget] = useState<T.Host | null>(null);
  const remove = useOperationMutation((confirm: string) => api.hosts.remove(removeTarget?.name ?? "", confirm));
  if (query.isPending) return <DomainLoading label="hosts" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const hosts = query.data.hosts;
  const current = hosts.find((host) => host.name === editing);
  return (
    <>
      <div className="box">
        <div className="tiles">
          {hosts.map((host) => (
            <HostTile
              key={host.name}
              host={host}
              onEdit={() => setEditing(host.name)}
              onRemove={() => setRemoveTarget(host)}
              onTraffic={() => onTraffic(host.name)}
            />
          ))}
          <button type="button" className="cell tile tile--add" onClick={() => setEditing("new")}>
            {hosts.length === 0 ? <Waypoints aria-hidden="true" /> : <Plus aria-hidden="true" />}
            {hosts.length === 0 ? "No hosts · Add host" : "Add host"}
          </button>
        </div>
      </div>
      <Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent>
          {editing !== null && <HostDialogBody key={editing} host={current} onDone={() => setEditing(null)} />}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={removeTarget !== null}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
        title={`Remove ${removeTarget?.name ?? ""}?`}
        description="The edge stops serving this host. Its target is not changed."
        phrase={removeTarget ? `delete ${removeTarget.name}` : ""}
        destructive
        confirmLabel="Remove host"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={(typed) => remove.mutate(typed, { onSuccess: () => setRemoveTarget(null) })}
      />
    </>
  );
}

/** ACME certificate state as a checklist fact: icon + word, then the time that matters. */
function CertificateText({ certificate }: { certificate: T.HostCertificate }) {
  switch (certificate.state) {
    case "issued":
      return <>✓ Valid · expires {formatRelative(certificate.expiresAt)}</>;
    case "requesting":
      return <>◌ Requesting</>;
    case "waiting":
      return (
        <span className="text-[var(--tamago-ink)]" title="Save the host to retry now">
          ▲ No cert · retry {formatRelative(certificate.retryAt)}
        </span>
      );
  }
}

function HostTile({
  host,
  onEdit,
  onRemove,
  onTraffic,
}: {
  host: T.Host;
  onEdit: () => void;
  onRemove: () => void;
  onTraffic: () => void;
}) {
  const scheme = host.route.tls === "none" ? "http" : "https";
  const redirect = host.target.kind === "redirect";
  return (
    <article className="cell tile" aria-label={host.name}>
      <div className="tile__top">
        <span className="mono mono--lg" aria-hidden="true">
          {host.name.slice(0, 1).toUpperCase()}
        </span>
        <div className="tile__name">
          <strong className="truncate" title={host.name}>
            {host.name}
          </strong>
          <small>
            <a href={`${scheme}://${host.name}`} target="_blank" rel="noreferrer">
              Open <ExternalLink className="inline size-3" aria-hidden="true" />
            </a>
          </small>
        </div>
        <StateBadge
          state={host.live ? "published" : "stopped"}
          label={host.live ? "Live" : host.enabled ? "Not live" : "Disabled"}
        />
      </div>
      <dl className="kv">
        <div>
          <dt>Target</dt>
          <dd>
            <HostTargetText host={host} />
          </dd>
        </div>
      </dl>
      {host.target.kind === "upstream" && (
        <code className="block truncate rounded-[0.625rem] bg-background px-3 py-2 text-xs">
          {(host.target.upstreams ?? []).join("  ")}
        </code>
      )}
      <dl className="kv">
        <div>
          <dt>TLS</dt>
          <dd>{tlsLabels[host.route.tls]}</dd>
        </div>
        {host.certificate && (
          <div>
            <dt>Certificate</dt>
            <dd>
              <CertificateText certificate={host.certificate} />
            </dd>
          </div>
        )}
        {!redirect && (
          <div>
            <dt>HTTPS redirect</dt>
            <dd>{host.route.redirectHttps ? "✓ On" : "— Off"}</dd>
          </div>
        )}
        {!redirect && (
          <div>
            <dt>Static cache</dt>
            <dd>{host.route.staticCache ? "✓ On" : "— Off"}</dd>
          </div>
        )}
        {!redirect && host.route.maxBodyMb ? (
          <div>
            <dt>Body limit</dt>
            <dd>{host.route.maxBodyMb} MB</dd>
          </div>
        ) : null}
        <div>
          <dt>Access log</dt>
          <dd>{host.route.accessLog ? "✓ On" : "— Off"}</dd>
        </div>
      </dl>
      <div className="flex items-center gap-2 border-t border-border pt-3">
        <Button variant="outline" onClick={onEdit}>
          <Pencil /> Edit
        </Button>
        {host.route.accessLog && (
          <Button variant="ghost" onClick={onTraffic}>
            <BarChart3 /> Traffic
          </Button>
        )}
        <Button variant="danger" size="icon" aria-label={`Remove ${host.name}`} onClick={onRemove}>
          <Trash2 />
        </Button>
      </div>
    </article>
  );
}
