import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Globe, Lock, Pencil, Plus, Trash2, Waypoints } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  Cell,
  CopyableCode,
  DomainError,
  DomainLoading,
  Field,
  KeyValues,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { Link } from "wouter";
import { useApplicationList, useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

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
      {query.data.state === "healthy" && <EdgeMetricsCell />}
      <EdgeForm status={query.data} />
      <SectionTitle title="Proxies" hint="Route edge domains to upstreams outside Bento." />
      <ProxiesPanel />
      <SectionTitle title="Routes" hint="What the edge is serving right now." />
      <RoutesBox status={query.data} />
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
    <div className="box">
      <Cell
        title="Traffic"
        action={
          m && (
            <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span className="size-1.5 animate-pulse rounded-full bg-emerald-500" />
              Live · 5s
            </span>
          )
        }
      >
        {query.error ? (
          <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
        ) : !m ? (
          <DomainLoading label="edge metrics" />
        ) : (
          <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <MetricCard
              label="Requests / s"
              value={m.requestsPerSecond.toFixed(1)}
              hint={`${compact(m.requests)} total`}
            />
            <MetricCard label="Connections" value={m.active} hint={`${m.acceptsPerSecond.toFixed(1)} new / s`} />
            <MetricCard
              label="Connection states"
              value={`${m.reading} · ${m.writing} · ${m.waiting}`}
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
  const valueColor =
    tone === "bad" ? "text-destructive" : tone === "good" ? "text-emerald-600 dark:text-emerald-400" : "";
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-[0.875rem] bg-background p-3">
      <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</span>
      <span className={`truncate text-2xl font-semibold tabular-nums ${valueColor}`}>{value}</span>
      {children}
      <span className="truncate text-xs text-muted-foreground">{hint}</span>
    </div>
  );
}

function StateBar({ reading, writing, waiting }: { reading: number; writing: number; waiting: number }) {
  const total = reading + writing + waiting || 1;
  const part = (n: number, color: string) => <span className={color} style={{ width: `${(n / total) * 100}%` }} />;
  return (
    <div className="flex h-1.5 overflow-hidden rounded-full bg-muted">
      {part(reading, "bg-sky-500")}
      {part(writing, "bg-amber-500")}
      {part(waiting, "bg-muted-foreground/40")}
    </div>
  );
}

const compactFormat = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });
function compact(n: number) {
  return compactFormat.format(n);
}

function SectionTitle({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="mb-2 mt-2 flex flex-wrap items-baseline gap-x-3 px-1">
      <h2 className="text-sm font-semibold">{title}</h2>
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </div>
  );
}

function EdgeForm({ status }: { status: T.EdgeStatus }) {
  const [settings, setSettings] = useState(status.settings);
  const save = useOperationMutation(() => api.edge.set(settings));
  const dirty = JSON.stringify(settings) !== JSON.stringify(status.settings);
  const set = (next: Partial<T.EdgeSettings>) => setSettings({ ...settings, ...next });
  return (
    <div className="box box--2">
      <Cell title="Edge" className="cell--wide" action={<StateBadge state={status.state} />}>
        <div className="grid gap-3 sm:grid-cols-2">
          <label className="check items-start rounded-[0.875rem] bg-background p-3">
            <Checkbox checked={settings.enabled} onCheckedChange={(checked) => set({ enabled: checked === true })} />
            <span className="grid gap-0.5">
              <span className="text-sm font-medium">Enabled</span>
              <span className="text-xs text-muted-foreground">
                Run the shared nginx edge for managed apps and proxies.
              </span>
            </span>
          </label>
          <label className="check items-start rounded-[0.875rem] bg-background p-3">
            <Checkbox checked={settings.http3} onCheckedChange={(checked) => set({ http3: checked === true })} />
            <span className="grid gap-0.5">
              <span className="text-sm font-medium">HTTP/3</span>
              <span className="text-xs text-muted-foreground">Advertise QUIC on the HTTPS port (UDP).</span>
            </span>
          </label>
        </div>
      </Cell>
      <Cell title="Listener">
        <div className="grid gap-4">
          <Field label="Bind address" hint="Interface the edge listens on.">
            <Input value={settings.bind} spellCheck={false} onChange={(event) => set({ bind: event.target.value })} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="HTTP port">
              <Input
                type="number"
                value={settings.httpPort}
                onChange={(event) => set({ httpPort: Number(event.target.value) })}
              />
            </Field>
            <Field label="HTTPS port">
              <Input
                type="number"
                value={settings.httpsPort}
                onChange={(event) => set({ httpsPort: Number(event.target.value) })}
              />
            </Field>
          </div>
        </div>
      </Cell>
      <Cell title="Certificates (ACME)">
        <div className="grid gap-4">
          <Field label="Contact email" hint="Used for expiry notices from the certificate authority.">
            <Input
              type="email"
              value={settings.acmeEmail}
              placeholder="ops@example.com"
              onChange={(event) => set({ acmeEmail: event.target.value })}
            />
          </Field>
          <Field label="Directory URL" hint="Leave the default for Let's Encrypt production.">
            <Input
              value={settings.acmeUrl}
              spellCheck={false}
              onChange={(event) => set({ acmeUrl: event.target.value })}
            />
          </Field>
        </div>
      </Cell>
      <Cell className="cell--wide">
        <div className="actions items-center">
          {save.error && <span className="note note--bad mr-auto">{messageOf(save.error)}</span>}
          {dirty && <span className="note">Unsaved changes</span>}
          <Button variant="outline" disabled={!dirty || save.isPending} onClick={() => setSettings(status.settings)}>
            Reset
          </Button>
          <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(undefined)}>
            Save
          </Button>
        </div>
      </Cell>
    </div>
  );
}

type RouteRow = {
  key: string;
  type: "App" | "Proxy";
  name: string;
  detail: string;
  domain: string;
  published: boolean;
  href?: string;
};

function RoutesBox({ status }: { status: T.EdgeStatus }) {
  const apps = useApplicationList();
  const proxies = useQuery({ queryKey: keys.proxies, queryFn: ({ signal }) => api.proxies.list(signal) });
  const active = new Set(status.routes);
  const rows: RouteRow[] = [];
  for (const app of apps.data?.apps ?? []) {
    if (app.ingress !== "managed") continue;
    rows.push({
      key: `app-${app.slug}`,
      type: "App",
      name: app.slug,
      detail: app.kind,
      domain: app.primaryDomain,
      published: active.has(`app-${app.slug}`),
      href: `/apps/${app.slug}`,
    });
  }
  for (const proxy of proxies.data?.proxies ?? []) {
    rows.push({
      key: `proxy-${proxy.name}`,
      type: "Proxy",
      name: proxy.name,
      detail: `${proxy.upstreams.length} upstream${proxy.upstreams.length === 1 ? "" : "s"}`,
      domain: proxy.domains.find((domain) => domain.primary)?.name ?? proxy.domains[0]?.name ?? "",
      published: active.has(`proxy-${proxy.name}`),
    });
  }
  // Routes the edge serves that no app or proxy claims (stale files).
  const known = new Set(rows.map((row) => row.key));
  for (const route of status.routes) {
    if (!known.has(route)) {
      rows.push({
        key: route,
        type: route.startsWith("proxy-") ? "Proxy" : "App",
        name: route,
        detail: "unknown",
        domain: "",
        published: true,
      });
    }
  }
  rows.sort((a, b) => Number(b.published) - Number(a.published) || a.name.localeCompare(b.name));
  const live = rows.filter((row) => row.published).length;
  return (
    <div className="box">
      <Cell title={`Routes · ${live} active`}>
        {apps.error || proxies.error ? (
          <DomainError
            message={messageOf(apps.error ?? proxies.error)}
            onRetry={() => {
              void apps.refetch();
              void proxies.refetch();
            }}
          />
        ) : rows.length === 0 ? (
          <p className="note">No apps or proxies use the edge yet.</p>
        ) : (
          <div className="grid divide-y">
            {rows.map((row) => (
              <div key={row.key} className="flex flex-wrap items-center gap-x-4 gap-y-1 py-2.5 first:pt-0 last:pb-0">
                <span className="w-14 shrink-0 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {row.type}
                </span>
                <div className="grid min-w-40 flex-1 gap-0.5">
                  {row.href ? (
                    <Link href={row.href} className="truncate text-sm font-medium">
                      {row.name}
                    </Link>
                  ) : (
                    <span className="truncate text-sm font-medium">{row.name}</span>
                  )}
                  <span className="text-xs text-muted-foreground">{row.detail}</span>
                </div>
                <code className="min-w-0 flex-1 truncate text-xs">{row.domain || "no domain"}</code>
                <StateBadge
                  state={row.published ? "published" : "stopped"}
                  label={row.published ? "Published" : "Not published"}
                />
              </div>
            ))}
          </div>
        )}
      </Cell>
    </div>
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
    <div className="box">
      <Cell
        title="Cloudflare Tunnel"
        action={query.data && <StateBadge state={query.data.enabled ? query.data.state : "absent"} />}
      >
        <div className="grid gap-4">
          {query.data?.note && <p className="note">{query.data.note}</p>}
          <div className="flex flex-wrap items-end gap-2">
            <div className="min-w-60 flex-1">
              <Field label="Token" hint="Stored privately, never shown again.">
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

type ProxyForm = { name: string; upstreams: string; domains: string; route: T.Route; enabled: boolean };
const blankProxy: ProxyForm = {
  name: "",
  upstreams: "",
  domains: "",
  route: { tls: "none", redirectHttps: false, accessLog: false, staticCache: false },
  enabled: true,
};
const tlsLabels: Record<T.TLSMode, string> = {
  none: "HTTP only",
  "self-signed": "Self-signed",
  acme: "ACME",
  external: "External cert",
};

function ProxiesPanel() {
  const query = useQuery({ queryKey: keys.proxies, queryFn: ({ signal }) => api.proxies.list(signal) });
  // null: dialog closed; "new" | proxy name: adding or editing.
  const [editing, setEditing] = useState<string | null>(null);
  const [removeTarget, setRemoveTarget] = useState<T.Proxy | null>(null);
  const remove = useOperationMutation((confirm: string) => api.proxies.remove(removeTarget?.name ?? "", confirm));
  if (query.isPending) return <DomainLoading label="proxies" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const proxies = query.data.proxies;
  const current = proxies.find((proxy) => proxy.name === editing);
  return (
    <>
      <div className="box">
        <div className="tiles">
          {proxies.map((proxy) => (
            <ProxyTile
              key={proxy.id}
              proxy={proxy}
              onEdit={() => setEditing(proxy.name)}
              onRemove={() => setRemoveTarget(proxy)}
            />
          ))}
          <button type="button" className="cell tile tile--add" onClick={() => setEditing("new")}>
            {proxies.length === 0 ? <Waypoints aria-hidden="true" /> : <Plus aria-hidden="true" />}
            {proxies.length === 0 ? "No proxies yet · Add one" : "Add proxy"}
          </button>
        </div>
      </div>
      <Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent>
          {editing !== null && <ProxyDialogBody key={editing} proxy={current} onDone={() => setEditing(null)} />}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={removeTarget !== null}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
        title={`Remove ${removeTarget?.name ?? ""}?`}
        description="Its edge routes are removed permanently."
        phrase={removeTarget ? `delete ${removeTarget.name}` : ""}
        destructive
        confirmLabel="Remove proxy"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={(typed) => remove.mutate(typed, { onSuccess: () => setRemoveTarget(null) })}
      />
    </>
  );
}

function ProxyTile({ proxy, onEdit, onRemove }: { proxy: T.Proxy; onEdit: () => void; onRemove: () => void }) {
  const primary = proxy.domains.find((domain) => domain.primary) ?? proxy.domains[0];
  const scheme = proxy.route.tls === "none" ? "http" : "https";
  const options = [
    proxy.route.redirectHttps && "HTTPS redirect",
    proxy.route.staticCache && "Static cache",
    proxy.route.accessLog && "Access log",
  ].filter(Boolean);
  return (
    <article className="cell tile" aria-label={proxy.name}>
      <div className="tile__top">
        <span className="mono mono--lg" aria-hidden="true">
          {proxy.name.slice(0, 1).toUpperCase()}
        </span>
        <div className="tile__name">
          <strong>{proxy.name}</strong>
          <small>
            {primary ? (
              <a href={`${scheme}://${primary.name}`} target="_blank" rel="noreferrer">
                {primary.name} <ExternalLink className="inline size-3" aria-hidden="true" />
              </a>
            ) : (
              "No domain"
            )}
          </small>
        </div>
        <StateBadge state={proxy.enabled ? "published" : "stopped"} label={proxy.enabled ? "Enabled" : "Disabled"} />
      </div>
      <dl className="facts">
        <div className="facts__wide">
          <dt>Upstreams</dt>
          <dd>
            <span className="chips">
              {proxy.upstreams.map((upstream) => (
                <code key={upstream} className="chip">
                  {upstream}
                </code>
              ))}
            </span>
          </dd>
        </div>
        <div>
          <dt>TLS</dt>
          <dd className="inline-flex items-center gap-1">
            {proxy.route.tls === "none" ? <Globe aria-hidden="true" /> : <Lock aria-hidden="true" />}
            {tlsLabels[proxy.route.tls]}
          </dd>
        </div>
        <div>
          <dt>Domains</dt>
          <dd title={proxy.domains.map((domain) => domain.name).join(", ")}>{proxy.domains.length}</dd>
        </div>
      </dl>
      <div className="flex items-center gap-1">
        <span className="tags mr-auto">
          {options.map((option) => (
            <span key={option as string} className="tag">
              {option}
            </span>
          ))}
        </span>
        <button type="button" className="icon-btn" aria-label={`Edit ${proxy.name}`} onClick={onEdit}>
          <Pencil />
        </button>
        <button type="button" className="icon-btn" aria-label={`Remove ${proxy.name}`} onClick={onRemove}>
          <Trash2 />
        </button>
      </div>
    </article>
  );
}

function ProxyDialogBody({ proxy, onDone }: { proxy?: T.Proxy; onDone: () => void }) {
  const [form, setForm] = useState<ProxyForm>(
    proxy
      ? {
          name: proxy.name,
          upstreams: proxy.upstreams.join(" "),
          domains: proxy.domains.map((domain) => domain.name).join(" "),
          route: proxy.route,
          enabled: proxy.enabled,
        }
      : blankProxy,
  );
  const route = form.route;
  const setRoute = (next: Partial<T.Route>) => setForm({ ...form, route: { ...route, ...next } });
  const save = useOperationMutation(() =>
    api.proxies.upsert({
      name: form.name.trim(),
      upstreams: form.upstreams.split(/\s+/).filter(Boolean),
      domains: form.domains.split(/[\s,]+/).filter(Boolean),
      route: route.tls === "none" ? { ...route, redirectHttps: false } : route,
      enabled: form.enabled,
    }),
  );
  return (
    <form
      className="grid gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(undefined, { onSuccess: onDone });
      }}
    >
      <DialogHeader>
        <DialogTitle>{proxy ? `Edit ${proxy.name}` : "Add proxy"}</DialogTitle>
        <DialogDescription>Route edge domains to an upstream outside Bento.</DialogDescription>
      </DialogHeader>
      <Field label="Name" hint={proxy ? "The name identifies the proxy and cannot change." : undefined}>
        <Input
          value={form.name}
          disabled={proxy !== undefined}
          spellCheck={false}
          autoFocus={!proxy}
          onChange={(event) => setForm({ ...form, name: event.target.value })}
        />
      </Field>
      <Field label="Upstreams" hint="Space-separated; several are load-balanced.">
        <Input
          placeholder="http://10.0.0.5:8080"
          spellCheck={false}
          value={form.upstreams}
          onChange={(event) => setForm({ ...form, upstreams: event.target.value })}
        />
      </Field>
      <Field label="Domains">
        <Input
          placeholder="a.example.com b.example.com"
          spellCheck={false}
          value={form.domains}
          onChange={(event) => setForm({ ...form, domains: event.target.value })}
        />
      </Field>
      <div className={route.tls === "external" ? "grid-2" : undefined}>
        <Field label="TLS">
          <NativeSelect
            className="w-full"
            value={route.tls}
            onChange={(event) => {
              const tls = event.target.value as T.TLSMode;
              setRoute({ tls, certName: tls === "external" ? route.certName : undefined });
            }}
          >
            <option value="none">None</option>
            <option value="self-signed">Self-signed</option>
            <option value="acme">ACME</option>
            <option value="external">External certificate</option>
          </NativeSelect>
        </Field>
        {route.tls === "external" && (
          <Field label="Certificate name">
            <Input
              value={route.certName ?? ""}
              spellCheck={false}
              onChange={(event) => setRoute({ certName: event.target.value })}
            />
          </Field>
        )}
      </div>
      <div className="flex flex-wrap gap-5">
        <label className="check">
          <Checkbox
            checked={form.enabled}
            onCheckedChange={(checked) => setForm({ ...form, enabled: checked === true })}
          />
          Enabled
        </label>
        <label className="check">
          <Checkbox
            checked={route.redirectHttps}
            disabled={route.tls === "none"}
            onCheckedChange={(checked) => setRoute({ redirectHttps: checked === true })}
          />
          HTTPS redirect
        </label>
        <label className="check">
          <Checkbox
            checked={route.accessLog}
            onCheckedChange={(checked) => setRoute({ accessLog: checked === true })}
          />
          Access log
        </label>
        <label
          className="check"
          title="Cache public static files (css, js, images, fonts) at the edge. Responses the upstream marks private, no-store, or that set cookies are never cached."
        >
          <Checkbox
            checked={route.staticCache ?? false}
            onCheckedChange={(checked) => setRoute({ staticCache: checked === true })}
          />
          Edge static cache
        </label>
      </div>
      {save.error && <Alert variant="destructive">{messageOf(save.error)}</Alert>}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!form.name.trim() || !form.upstreams.trim() || save.isPending}>
          {proxy ? "Save" : "Add proxy"}
        </Button>
      </DialogFooter>
    </form>
  );
}
