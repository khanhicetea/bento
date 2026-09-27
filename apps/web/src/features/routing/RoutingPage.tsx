import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
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

export function RoutingPage() {
  return (
    <Page>
      <PageHeader
        section="Ingress"
        title="Ingress"
        description="The managed edge is optional. Apps can also be reached directly by cloudflared or an operator-owned proxy on the app network; those routes are operator-owned."
      />
      <EdgePanel />
      <TunnelPanel />
      <ProxiesPanel />
    </Page>
  );
}

function EdgePanel() {
  const q = useQuery({ queryKey: keys.edge, queryFn: ({ signal }) => api.edge.get(signal) });
  if (q.isPending) return <DomainLoading label="edge" />;
  if (q.error) return <DomainError message={messageOf(q.error)} onRetry={() => void q.refetch()} />;
  return <EdgeForm key={JSON.stringify(q.data.settings)} status={q.data} />;
}

function EdgeForm({ status }: { status: T.EdgeStatus }) {
  const [s, setS] = useState<T.EdgeSettings>(status.settings);
  const save = useOperationMutation(() => api.edge.set(s));
  return (
    <Panel
      title="Managed edge (Nginx)"
      description="Publishes the chosen HTTP/HTTPS host ports and routes published apps by network alias. Bypassing it also bypasses its TLS policy, redirects, limits, and logs."
      actions={<StateBadge state={status.state} />}
    >
      <div className="grid grid-cols-4 gap-4 max-[900px]:grid-cols-2">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={s.enabled} onChange={(e) => setS({ ...s, enabled: e.target.checked })} />{" "}
          Enabled
        </label>
        <Field label="Bind address">
          <Input value={s.bind} onChange={(e) => setS({ ...s, bind: e.target.value })} />
        </Field>
        <Field label="HTTP port">
          <Input type="number" value={s.httpPort} onChange={(e) => setS({ ...s, httpPort: Number(e.target.value) })} />
        </Field>
        <Field label="HTTPS port">
          <Input
            type="number"
            value={s.httpsPort}
            onChange={(e) => setS({ ...s, httpsPort: Number(e.target.value) })}
          />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={s.http3} onChange={(e) => setS({ ...s, http3: e.target.checked })} /> HTTP/3
          (UDP)
        </label>
        <Field label="ACME email">
          <Input value={s.acmeEmail} onChange={(e) => setS({ ...s, acmeEmail: e.target.value })} />
        </Field>
        <Field label="ACME directory">
          <Input value={s.acmeUrl} onChange={(e) => setS({ ...s, acmeUrl: e.target.value })} />
        </Field>
      </div>
      <div className="mt-4 flex items-center gap-3">
        <Button onClick={() => save.mutate(undefined)} disabled={save.isPending}>
          Apply edge settings
        </Button>
        <span className="text-xs text-muted-foreground">Active routes: {status.routes.join(", ") || "none"}</span>
      </div>
      {save.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(save.error)}
        </Alert>
      )}
    </Panel>
  );
}

function TunnelPanel() {
  const q = useQuery({ queryKey: keys.tunnel, queryFn: ({ signal }) => api.tunnel.get(signal) });
  const [token, setToken] = useState("");
  const set = useOperationMutation((value: string) => api.tunnel.setToken(value));
  return (
    <Panel
      title="Cloudflare Tunnel"
      description={q.data?.note}
      actions={q.data && <StateBadge state={q.data.enabled ? q.data.state : "absent"} />}
    >
      <div className="flex flex-wrap items-end gap-2">
        <Field
          label="Replace token"
          hint="Stored in a private file; never returned. Replacing it recreates only the tunnel container."
        >
          <Input
            type="password"
            className="w-96 max-w-full"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="off"
          />
        </Field>
        <Button disabled={!token || set.isPending} onClick={() => set.mutate(token, { onSuccess: () => setToken("") })}>
          Save token
        </Button>
        {q.data?.enabled && (
          <Button variant="outline" onClick={() => set.mutate("")}>
            Disable tunnel
          </Button>
        )}
      </div>
      {set.error && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(set.error)}
        </Alert>
      )}
    </Panel>
  );
}

function ProxiesPanel() {
  const q = useQuery({ queryKey: keys.proxies, queryFn: ({ signal }) => api.proxies.list(signal) });
  const [form, setForm] = useState({ name: "", upstreams: "", domains: "", tls: "none" as T.TLSMode });
  const save = useOperationMutation(() =>
    api.proxies.upsert({
      name: form.name,
      upstreams: form.upstreams.split(/\s+/).filter(Boolean),
      domains: form.domains.split(/[\s,]+/).filter(Boolean),
      route: { tls: form.tls, redirectHttps: false, accessLog: false },
      enabled: true,
    }),
  );
  const remove = useOperationMutation((name: string) => api.proxies.remove(name, `delete ${name}`));
  return (
    <Panel title="Reverse proxies" description="Edge routes to upstreams outside Bento's lifecycle.">
      {(q.data?.proxies ?? []).length === 0 ? (
        <EmptyPanel>No reverse proxies.</EmptyPanel>
      ) : (
        <div className="mb-4 grid gap-2">
          {q.data?.proxies.map((p) => (
            <div key={p.id} className="flex items-center justify-between rounded-md border border-border p-3 text-sm">
              <span>
                <strong>{p.name}</strong> · {p.domains.map((d) => d.name).join(", ")} → {p.upstreams.join(", ")}
              </span>
              <Button size="xs" variant="destructive" onClick={() => remove.mutate(p.name)}>
                Remove
              </Button>
            </div>
          ))}
        </div>
      )}
      <div className="grid grid-cols-4 items-end gap-3 max-[900px]:grid-cols-1">
        <Field label="Name">
          <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
        </Field>
        <Field label="Upstream URLs">
          <Input
            value={form.upstreams}
            placeholder="http://10.0.0.5:8080"
            onChange={(e) => setForm({ ...form, upstreams: e.target.value })}
          />
        </Field>
        <Field label="Domains">
          <Input value={form.domains} onChange={(e) => setForm({ ...form, domains: e.target.value })} />
        </Field>
        <Field label="TLS">
          <NativeSelect value={form.tls} onChange={(e) => setForm({ ...form, tls: e.target.value as T.TLSMode })}>
            <option value="none">None</option>
            <option value="self-signed">Self-signed</option>
            <option value="acme">ACME</option>
          </NativeSelect>
        </Field>
      </div>
      <Button className="mt-3" onClick={() => save.mutate(undefined)} disabled={save.isPending || !form.name}>
        Save proxy
      </Button>
      {(save.error || remove.error) && (
        <Alert variant="destructive" className="mt-3">
          {messageOf(save.error ?? remove.error)}
        </Alert>
      )}
    </Panel>
  );
}
