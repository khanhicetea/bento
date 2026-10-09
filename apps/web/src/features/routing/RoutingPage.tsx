import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, ExternalLink, Globe, Lock, Network, Pencil, Plus, Trash2, Waypoints } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
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
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

type Tab = "edge" | "tunnel" | "utils";
const tabLabels: Record<Tab, string> = { edge: "Edge", tunnel: "Tunnel", utils: "Utils" };

export function RoutingPage() {
  const [tab, setTab] = useState<Tab>("edge");
  return (
    <>
      <PageHeader title="Ingress" />
      <div className="seg mb-5" role="tablist" aria-label="Ingress sections">
        {(["edge", "tunnel", "utils"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "edge" && <EdgePanel />}
      {tab === "tunnel" && <TunnelPanel />}
      {tab === "utils" && <UtilsPanel />}
    </>
  );
}

function EdgePanel() {
  const query = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  if (query.isPending) return <DomainLoading label="edge" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return (
    <>
      <EdgeForm status={query.data} />
      <HostsPanel />
    </>
  );
}

function EdgeMetricsCell() {
  const query = useQuery({
    queryKey: keys.edgeMetrics,
    queryFn: ({ signal }) => api.edge.metrics(signal),
    refetchInterval: 5_000,
  });
  const m = query.data;
  return (
    <>
      <Cell
        title="Traffic"
        icon={<Activity />}
        className="flex flex-col"
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
          <div className="grid flex-1 grid-cols-2 gap-2 lg:grid-cols-4">
            <MetricCard
              label="Requests / s"
              value={m.requestsPerSecond.toFixed(1)}
              hint={`${compact(m.requests)} total`}
            />
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
        )}
      </Cell>
    </>
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
  const [settings, setSettings] = useState(status.settings);
  const save = useOperationMutation(() => api.edge.set(settings));
  const dirty = JSON.stringify(settings) !== JSON.stringify(status.settings);
  const set = (next: Partial<T.EdgeSettings>) => setSettings({ ...settings, ...next });
  return (
    <>
      <section className="box box--edge" aria-label="Edge">
        <Cell
          title="Edge"
          icon={<Network />}
          action={<StateBadge state={status.settings.enabled ? status.state : "absent"} />}
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
          </div>
        </Cell>
        {status.state === "healthy" ? (
          <EdgeMetricsCell />
        ) : (
          <Cell kind="kara" className="grid place-items-center">
            <EmptyState title="No traffic" body="Edge is not running." />
          </Cell>
        )}
      </section>
      <section className="box box--edge-routes" aria-label="Routes and certificates">
        <RoutesCell status={status} />
        <Cell title="Certificates · ACME" icon={<Lock />} kind={dirty ? "tamago" : "gohan"} className="flex flex-col">
          <div className="grid gap-4">
            <Field label="Contact email" hint="Expiry notices">
              <Input
                type="email"
                value={settings.acmeEmail}
                placeholder="ops@example.com"
                onChange={(event) => set({ acmeEmail: event.target.value })}
              />
            </Field>
            <Field label="Directory URL" hint="Default: Let's Encrypt production">
              <Input
                value={settings.acmeUrl}
                spellCheck={false}
                onChange={(event) => set({ acmeUrl: event.target.value })}
              />
            </Field>
          </div>
          <div className="mt-auto grid gap-2 border-t border-foreground/10 pt-3">
            {dirty && <span className="label text-xs text-[var(--tamago-ink)]">● Unsaved edge settings</span>}
            {save.error && <span className="note note--bad">{messageOf(save.error)}</span>}
            <div className="flex gap-2">
              <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(undefined)}>
                Save
              </Button>
              <Button variant="ghost" disabled={!dirty || save.isPending} onClick={() => setSettings(status.settings)}>
                Reset
              </Button>
            </div>
          </div>
        </Cell>
      </section>
    </>
  );
}

function RoutesCell({ status }: { status: T.EdgeStatus }) {
  const hosts = useQuery({ queryKey: keys.hosts, queryFn: ({ signal }) => api.hosts.list(signal) });
  const rows = [...(hosts.data?.hosts ?? [])];
  // Routes the edge serves that no host claims (stale files).
  const known = new Set(rows.map((host) => `host-${host.name}`));
  const stale = status.routes.filter((route) => !known.has(route));
  rows.sort((a, b) => Number(b.live) - Number(a.live) || a.name.localeCompare(b.name));
  const live = rows.filter((host) => host.live).length + stale.length;
  return (
    <Cell
      title={`Routes · ${live} live`}
      icon={<Globe />}
      action={<span className="label text-xs text-muted-foreground">Serving now</span>}
    >
      {hosts.error ? (
        <DomainError message={messageOf(hosts.error)} onRetry={() => void hosts.refetch()} />
      ) : rows.length === 0 && stale.length === 0 ? (
        <EmptyState title="No routes" body="No hosts use the edge yet." />
      ) : (
        <div className="overflow-x-auto">
          <table className="routes">
            <thead>
              <tr>
                <th>Host</th>
                <th>Target</th>
                <th>State</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((host) => (
                <tr key={host.name}>
                  <td className="font-mono">{host.name}</td>
                  <td>
                    <HostTargetText host={host} />
                  </td>
                  <td>
                    <StateBadge
                      state={host.live ? "published" : "stopped"}
                      label={host.live ? "Live" : host.enabled ? "Not live" : "Disabled"}
                    />
                  </td>
                </tr>
              ))}
              {stale.map((route) => (
                <tr key={route}>
                  <td className="font-mono">{route}</td>
                  <td>—</td>
                  <td>
                    <StateBadge state="published" label="Live · unknown" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
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

function HostsPanel() {
  const query = useQuery({ queryKey: keys.hosts, queryFn: ({ signal }) => api.hosts.list(signal) });
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

function HostTile({ host, onEdit, onRemove }: { host: T.Host; onEdit: () => void; onRemove: () => void }) {
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
        <div>
          <dt>Access log</dt>
          <dd>{host.route.accessLog ? "✓ On" : "— Off"}</dd>
        </div>
      </dl>
      <div className="flex items-center gap-2 border-t border-border pt-3">
        <Button variant="outline" onClick={onEdit}>
          <Pencil /> Edit
        </Button>
        <Button variant="danger" size="icon" aria-label={`Remove ${host.name}`} onClick={onRemove}>
          <Trash2 />
        </Button>
      </div>
    </article>
  );
}
