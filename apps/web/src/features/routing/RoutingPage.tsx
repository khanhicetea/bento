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
import { useOperationMutation } from "../applications/useApplications.ts";
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

type Tab = "edge" | "tunnel" | "proxies" | "utils";
const tabLabels: Record<Tab, string> = { edge: "Edge", tunnel: "Tunnel", proxies: "Proxies", utils: "Utils" };

export function RoutingPage() {
  const [tab, setTab] = useState<Tab>("edge");
  return (
    <>
      <PageHeader title="Ingress" />
      <div className="seg mb-5" role="tablist" aria-label="Ingress sections">
        {(["edge", "tunnel", "proxies", "utils"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "edge" && <EdgePanel />}
      {tab === "tunnel" && <TunnelPanel />}
      {tab === "proxies" && <ProxiesPanel />}
      {tab === "utils" && <UtilsPanel />}
    </>
  );
}

function EdgePanel() {
  const query = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  if (query.isPending) return <DomainLoading label="edge" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return <EdgeForm status={query.data} />;
}

function EdgeForm({ status }: { status: T.EdgeStatus }) {
  const [settings, setSettings] = useState(status.settings);
  const save = useOperationMutation(() => api.edge.set(settings));
  const dirty = JSON.stringify(settings) !== JSON.stringify(status.settings);
  return (
    <div className="box box--main">
      <Cell title="Edge" action={<StateBadge state={status.state} />}>
        <div className="grid gap-4">
          <div className="flex flex-wrap gap-5">
            <label className="check">
              <Checkbox
                checked={settings.enabled}
                onCheckedChange={(checked) => setSettings({ ...settings, enabled: checked === true })}
              />
              Enabled
            </label>
            <label className="check">
              <Checkbox
                checked={settings.http3}
                onCheckedChange={(checked) => setSettings({ ...settings, http3: checked === true })}
              />
              HTTP/3
            </label>
          </div>
          <div className="grid-3">
            <Field label="Bind">
              <Input
                value={settings.bind}
                onChange={(event) => setSettings({ ...settings, bind: event.target.value })}
              />
            </Field>
            <Field label="HTTP">
              <Input
                type="number"
                value={settings.httpPort}
                onChange={(event) => setSettings({ ...settings, httpPort: Number(event.target.value) })}
              />
            </Field>
            <Field label="HTTPS">
              <Input
                type="number"
                value={settings.httpsPort}
                onChange={(event) => setSettings({ ...settings, httpsPort: Number(event.target.value) })}
              />
            </Field>
          </div>
          <div className="grid-2">
            <Field label="ACME email">
              <Input
                value={settings.acmeEmail}
                onChange={(event) => setSettings({ ...settings, acmeEmail: event.target.value })}
              />
            </Field>
            <Field label="ACME directory">
              <Input
                value={settings.acmeUrl}
                onChange={(event) => setSettings({ ...settings, acmeUrl: event.target.value })}
              />
            </Field>
          </div>
          <div className="actions items-center">
            {save.error && <span className="note note--bad mr-auto">{messageOf(save.error)}</span>}
            {dirty && <span className="note">Unsaved</span>}
            <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(undefined)}>
              Save
            </Button>
          </div>
        </div>
      </Cell>
      <Cell title={`Routes · ${status.routes.length}`}>
        {status.routes.length === 0 ? (
          <p className="note">None active</p>
        ) : (
          <div className="grid gap-1.5">
            {status.routes.map((route) => (
              <code key={route} className="chip justify-start truncate rounded-md!">
                {route}
              </code>
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
            hint="Where your ingress exposes Bento's utils routes (/_bento/webhook/*, /_bento/dbadmin/*, /_bento/scheduler/*). Used for full webhook URLs, database browser, and scheduler links."
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
          Route <code>/_bento/webhook/*</code>, <code>/_bento/dbadmin/*</code>, and <code>/_bento/scheduler/*</code> on
          that host to one of these. The edge forwards <code>/_bento/webhook/*</code> automatically for its domains.
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
