import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Pencil, Trash2 } from "lucide-react";
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
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

type Tab = "edge" | "tunnel" | "proxies" | "public";
const tabLabels: Record<Tab, string> = { edge: "Edge", tunnel: "Tunnel", proxies: "Proxies", public: "Public URL" };

export function RoutingPage() {
  const [tab, setTab] = useState<Tab>("edge");
  return (
    <>
      <PageHeader title="Ingress" />
      <div className="seg mb-5" role="tablist" aria-label="Ingress sections">
        {(["edge", "tunnel", "proxies", "public"] as const).map((value) => (
          <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => setTab(value)}>
            {tabLabels[value]}
          </button>
        ))}
      </div>
      {tab === "edge" && <EdgePanel />}
      {tab === "tunnel" && <TunnelPanel />}
      {tab === "proxies" && <ProxiesPanel />}
      {tab === "public" && <PublicPanel />}
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

function PublicPanel() {
  const query = useQuery({ queryKey: keys.public, queryFn: ({ signal }) => api.public.get(signal) });
  if (query.isPending) return <DomainLoading label="public URL" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  return <PublicForm settings={query.data} />;
}

function PublicForm({ settings }: { settings: T.PublicSettings }) {
  const queryClient = useQueryClient();
  const [baseUrl, setBaseUrl] = useState(settings.baseUrl);
  const save = useMutation({
    mutationFn: () => api.public.set({ baseUrl: baseUrl.trim() }),
    onSuccess: (next) => {
      queryClient.setQueryData(keys.public, next);
      setBaseUrl(next.baseUrl);
      void queryClient.invalidateQueries({ queryKey: keys.apps.webhooks });
    },
  });
  return (
    <div className="box box--2">
      <Cell title="Public base URL">
        <form
          className="grid gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            save.mutate();
          }}
        >
          <Field
            label="Base URL"
            hint="Where your ingress exposes Bento's public routes (/_webhook/*). Used to show full webhook URLs."
          >
            <Input
              value={baseUrl}
              placeholder="https://hooks.example.com"
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
      <Cell title="Public listener" className="cell--muted">
        <p className="note mb-3">
          Route <code>/_webhook/*</code> on that host to one of these. The edge does it automatically for its domains.
        </p>
        {settings.targets.length ? (
          <KeyValues
            items={settings.targets.map((target): [string, React.ReactNode] => [
              target.includes("127.0.0.1") || target.includes("[::1]") ? "Host proxy" : "Tunnel / containers",
              <CopyableCode value={target} />,
            ])}
          />
        ) : (
          <p className="note">The public listener is off (bento serve --public-listen off).</p>
        )}
      </Cell>
    </div>
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

function ProxiesPanel() {
  const query = useQuery({ queryKey: keys.proxies, queryFn: ({ signal }) => api.proxies.list(signal) });
  const [form, setForm] = useState({ name: "", upstreams: "", domains: "", tls: "none" as T.TLSMode });
  const [removeTarget, setRemoveTarget] = useState<T.Proxy | null>(null);
  const save = useOperationMutation(() =>
    api.proxies.upsert({
      name: form.name,
      upstreams: form.upstreams.split(/\s+/).filter(Boolean),
      domains: form.domains.split(/[\s,]+/).filter(Boolean),
      route: { tls: form.tls, redirectHttps: false, accessLog: false },
      enabled: true,
    }),
  );
  const remove = useOperationMutation((confirm: string) => api.proxies.remove(removeTarget?.name ?? "", confirm));
  function edit(proxy: T.Proxy) {
    setForm({
      name: proxy.name,
      upstreams: proxy.upstreams.join(" "),
      domains: proxy.domains.map((domain) => domain.name).join(" "),
      tls: proxy.route.tls,
    });
  }
  const proxies = query.data?.proxies ?? [];
  return (
    <div className="box box--main">
      <Cell title="Proxies">
        {query.error ? (
          <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
        ) : proxies.length === 0 ? (
          <p className="note">None yet</p>
        ) : (
          <div className="rows rows--lined">
            {proxies.map((proxy) => (
              <div key={proxy.id} className="row">
                <span className="row__main">
                  <strong>{proxy.name}</strong>
                  <small className="flex items-center gap-1">
                    {proxy.domains.map((domain) => domain.name).join(", ") || "—"}
                    <ArrowRight className="size-3 shrink-0" />
                    {proxy.upstreams.join(", ")}
                  </small>
                </span>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={`Edit ${proxy.name}`}
                  onClick={() => edit(proxy)}
                >
                  <Pencil />
                </button>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={`Remove ${proxy.name}`}
                  onClick={() => setRemoveTarget(proxy)}
                >
                  <Trash2 />
                </button>
              </div>
            ))}
          </div>
        )}
      </Cell>
      <Cell title="Add or update" className="cell--muted">
        <div className="grid gap-3">
          <Field label="Name">
            <Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
          </Field>
          <Field label="Upstreams">
            <Input
              placeholder="http://10.0.0.5:8080"
              value={form.upstreams}
              onChange={(event) => setForm({ ...form, upstreams: event.target.value })}
            />
          </Field>
          <Field label="Domains">
            <Input
              placeholder="a.example.com b.example.com"
              value={form.domains}
              onChange={(event) => setForm({ ...form, domains: event.target.value })}
            />
          </Field>
          <Field label="TLS">
            <NativeSelect
              className="w-full"
              value={form.tls}
              onChange={(event) => setForm({ ...form, tls: event.target.value as T.TLSMode })}
            >
              <option value="none">None</option>
              <option value="self-signed">Self-signed</option>
              <option value="acme">ACME</option>
            </NativeSelect>
          </Field>
          <Button
            disabled={!form.name || !form.upstreams.trim() || save.isPending}
            onClick={() => save.mutate(undefined)}
          >
            Save proxy
          </Button>
          {save.error && <p className="note note--bad">{messageOf(save.error)}</p>}
        </div>
      </Cell>
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
    </div>
  );
}
