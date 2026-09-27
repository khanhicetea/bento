import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  DomainError,
  DomainLoading,
  EmptyState,
  Field,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
import { useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

type Tab = "edge" | "tunnel" | "proxies";
export function RoutingPage() {
  const [tab, setTab] = useState<Tab>("edge");
  return (
    <Page>
      <PageHeader title="Ingress" description="Managed edge, Cloudflare Tunnel, and operator-owned reverse proxies." />
      <div className="mb-6 flex gap-1 border-b" role="tablist">
        {(["edge", "tunnel", "proxies"] as const).map((value) => (
          <Button
            key={value}
            role="tab"
            aria-selected={tab === value}
            variant={tab === value ? "default" : "ghost"}
            onClick={() => setTab(value)}
          >
            {value[0]?.toUpperCase() + value.slice(1)}
          </Button>
        ))}
      </div>
      {tab === "edge" && <EdgePanel />}
      {tab === "tunnel" && <TunnelPanel />}
      {tab === "proxies" && <ProxiesPanel />}
    </Page>
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
    <>
      <Panel
        title="Managed edge"
        description="Listener and ACME settings. Saving validates a candidate configuration before reload."
        actions={<StateBadge state={status.state} />}
      >
        <div className="grid gap-4 sm:grid-cols-2">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={settings.enabled}
              onChange={(event) => setSettings({ ...settings, enabled: event.target.checked })}
            />{" "}
            Enabled
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={settings.http3}
              onChange={(event) => setSettings({ ...settings, http3: event.target.checked })}
            />{" "}
            HTTP/3 (UDP)
          </label>
          <Field label="Bind address">
            <Input value={settings.bind} onChange={(event) => setSettings({ ...settings, bind: event.target.value })} />
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <Field label="HTTP port">
              <Input
                type="number"
                value={settings.httpPort}
                onChange={(event) => setSettings({ ...settings, httpPort: Number(event.target.value) })}
              />
            </Field>
            <Field label="HTTPS port">
              <Input
                type="number"
                value={settings.httpsPort}
                onChange={(event) => setSettings({ ...settings, httpsPort: Number(event.target.value) })}
              />
            </Field>
          </div>
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
        <div className="mt-4 flex items-center gap-3">
          <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(undefined)}>
            Save settings
          </Button>
          {dirty && <span className="text-xs text-warning">Unsaved changes</span>}
        </div>
        {save.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(save.error)}
          </Alert>
        )}
      </Panel>
      <Panel title="Active routes">
        {status.routes.length === 0 ? (
          <EmptyState title="No active managed routes" />
        ) : (
          <div className="grid gap-2">
            {status.routes.map((route) => (
              <code key={route} className="rounded bg-muted p-2 text-xs">
                {route}
              </code>
            ))}
          </div>
        )}
      </Panel>
    </>
  );
}
function TunnelPanel() {
  const query = useQuery({ queryKey: keys.tunnel, queryFn: ({ signal }) => api.tunnel.get(signal) });
  const [token, setToken] = useState("");
  const [disableOpen, setDisableOpen] = useState(false);
  const mutation = useOperationMutation((value: string) => api.tunnel.setToken(value));
  return (
    <Panel
      title="Cloudflare Tunnel"
      description={query.data?.note}
      actions={query.data && <StateBadge state={query.data.enabled ? query.data.state : "absent"} />}
    >
      <div className="flex flex-wrap items-end gap-2">
        <Field label="Replace token" hint="Stored privately and never returned by the API.">
          <Input
            className="w-96 max-w-full"
            type="password"
            autoComplete="off"
            value={token}
            onChange={(event) => setToken(event.target.value)}
          />
        </Field>
        <Button
          disabled={!token || mutation.isPending}
          onClick={() => mutation.mutate(token, { onSuccess: () => setToken("") })}
        >
          Save token
        </Button>
        {query.data?.enabled && (
          <Button variant="outline" onClick={() => setDisableOpen(true)}>
            Disable tunnel…
          </Button>
        )}
      </div>
      {mutation.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(mutation.error)}
        </Alert>
      )}
      <ConfirmDialog
        open={disableOpen}
        onOpenChange={setDisableOpen}
        title="Disable Cloudflare Tunnel?"
        description="This removes tunnel connectivity. Operator-owned routes may become unreachable."
        confirmLabel="Disable tunnel"
        pending={mutation.isPending}
        error={mutation.error}
        onConfirm={() => mutation.mutate("", { onSuccess: () => setDisableOpen(false) })}
      />
    </Panel>
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
  return (
    <Panel title="Reverse proxies" description="Edge routes to upstreams outside Bento's lifecycle.">
      {(query.data?.proxies ?? []).length === 0 ? (
        <EmptyState title="No reverse proxies" />
      ) : (
        <div className="mb-5 grid gap-2">
          {query.data?.proxies.map((proxy) => (
            <div
              key={proxy.id}
              className="flex flex-wrap items-center justify-between gap-2 rounded-lg border p-3 text-sm"
            >
              <span>
                <strong>{proxy.name}</strong> · {proxy.domains.map((domain) => domain.name).join(", ")} →{" "}
                {proxy.upstreams.join(", ")}
              </span>
              <span className="flex gap-1">
                <Button size="xs" variant="outline" onClick={() => edit(proxy)}>
                  Edit
                </Button>
                <Button size="xs" variant="destructive" onClick={() => setRemoveTarget(proxy)}>
                  Remove…
                </Button>
              </span>
            </div>
          ))}
        </div>
      )}
      <div className="grid items-end gap-3 sm:grid-cols-2">
        <Field label="Name">
          <Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
        </Field>
        <Field label="Upstream URLs">
          <Input
            placeholder="http://10.0.0.5:8080"
            value={form.upstreams}
            onChange={(event) => setForm({ ...form, upstreams: event.target.value })}
          />
        </Field>
        <Field label="Domains">
          <Input value={form.domains} onChange={(event) => setForm({ ...form, domains: event.target.value })} />
        </Field>
        <Field label="TLS">
          <NativeSelect
            value={form.tls}
            onChange={(event) => setForm({ ...form, tls: event.target.value as T.TLSMode })}
          >
            <option value="none">None</option>
            <option value="self-signed">Self-signed</option>
            <option value="acme">ACME</option>
          </NativeSelect>
        </Field>
      </div>
      <Button
        className="mt-3"
        disabled={!form.name || !form.upstreams.trim() || save.isPending}
        onClick={() => save.mutate(undefined)}
      >
        Save proxy
      </Button>
      {save.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(save.error)}
        </Alert>
      )}
      <ConfirmDialog
        open={removeTarget !== null}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
        title={`Remove proxy ${removeTarget?.name ?? ""}?`}
        description="This permanently removes its managed edge routes."
        phrase={removeTarget ? `delete ${removeTarget.name}` : ""}
        destructive
        confirmLabel="Remove proxy"
        pending={remove.isPending}
        error={remove.error}
        onConfirm={(typed) => remove.mutate(typed, { onSuccess: () => setRemoveTarget(null) })}
      />
    </Panel>
  );
}
