import { useState } from "react";
import { messageOf, api, type T } from "../../api/client.ts";
import { Field } from "../../components/DomainState.tsx";
import { useApplicationList, useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

export const tlsLabels: Record<T.TLSMode, string> = {
  none: "HTTP only",
  "self-signed": "Self-signed",
  acme: "ACME",
  external: "External cert",
};

export const targetLabels: Record<T.HostTargetKind, string> = {
  app: "App",
  upstream: "Upstream",
  redirect: "Redirect",
};

type HostForm = {
  name: string;
  kind: T.HostTargetKind;
  app: string;
  upstreams: string;
  redirectTo: string;
  route: T.Route;
  enabled: boolean;
};

function formOf(host: T.Host | undefined, app: string | undefined): HostForm {
  if (host) {
    return {
      name: host.name,
      kind: host.target.kind,
      app: host.target.app ?? "",
      upstreams: (host.target.upstreams ?? []).join(" "),
      redirectTo: host.target.redirectTo ?? "",
      route: host.route,
      enabled: host.enabled,
    };
  }
  return {
    name: "",
    kind: app ? "app" : "upstream",
    app: app ?? "",
    upstreams: "",
    redirectTo: "",
    route: { tls: "none", redirectHttps: false, accessLog: false, staticCache: false },
    enabled: true,
  };
}

/**
 * Add or edit one Ingress host. Render inside a DialogContent; `app` presets
 * an app target for a new host. `compact` hides the target, access log and edge
 * cache fields, which keep their defaults and stay editable in Ingress.
 */
export function HostDialogBody({
  host,
  app,
  compact = false,
  onDone,
}: {
  host?: T.Host;
  app?: string;
  compact?: boolean;
  onDone: () => void;
}) {
  const apps = useApplicationList();
  const [form, setForm] = useState<HostForm>(() => formOf(host, app));
  const route = form.route;
  const set = (next: Partial<HostForm>) => setForm({ ...form, ...next });
  const setRoute = (next: Partial<T.Route>) => set({ route: { ...route, ...next } });
  const managedApps = (apps.data?.apps ?? []).filter((candidate) => candidate.ingress === "managed");
  const save = useOperationMutation(() => {
    const target: T.HostTarget = { kind: form.kind };
    if (form.kind === "app") target.app = form.app;
    if (form.kind === "upstream") target.upstreams = form.upstreams.split(/\s+/).filter(Boolean);
    if (form.kind === "redirect") target.redirectTo = form.redirectTo.trim();
    const body: T.HostRequest = {
      name: form.name.trim(),
      target,
      route:
        form.kind === "redirect"
          ? { ...route, redirectHttps: false, staticCache: false }
          : route.tls === "none"
            ? { ...route, redirectHttps: false }
            : route,
      enabled: form.enabled,
    };
    return host ? api.hosts.update(host.name, body) : api.hosts.create(body);
  });
  const targetSet =
    (form.kind === "app" && form.app !== "") ||
    (form.kind === "upstream" && form.upstreams.trim() !== "") ||
    (form.kind === "redirect" && form.redirectTo.trim() !== "");
  return (
    <form
      className="grid gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(undefined, { onSuccess: onDone });
      }}
    >
      <DialogHeader>
        <DialogTitle>{host ? `Edit ${host.name}` : compact ? "Add domain" : "Add host"}</DialogTitle>
        <DialogDescription>{"Point one domain at an app, an upstream, or another host."}</DialogDescription>
      </DialogHeader>
      <Field label="Host" hint={host ? "The host name cannot change." : undefined}>
        <Input
          value={form.name}
          placeholder="shop.example.com"
          disabled={host !== undefined}
          spellCheck={false}
          autoFocus={!host}
          onChange={(event) => set({ name: event.target.value })}
        />
      </Field>
      {!compact && (
        <div className="grid-2">
          <Field label="Target">
            <NativeSelect
              className="w-full"
              value={form.kind}
              onChange={(event) => set({ kind: event.target.value as T.HostTargetKind })}
            >
              <option value="app">App</option>
              <option value="upstream">Upstream</option>
              <option value="redirect">Redirect</option>
            </NativeSelect>
          </Field>
          {form.kind === "app" && (
            <Field label="App">
              <NativeSelect className="w-full" value={form.app} onChange={(event) => set({ app: event.target.value })}>
                <option value="" disabled>
                  {managedApps.length ? "Choose app" : "No managed apps"}
                </option>
                {managedApps.map((candidate) => (
                  <option key={candidate.id} value={candidate.slug}>
                    {candidate.slug}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
          {form.kind === "redirect" && (
            <Field label="Redirect to">
              <Input
                placeholder="example.com"
                spellCheck={false}
                value={form.redirectTo}
                onChange={(event) => set({ redirectTo: event.target.value })}
              />
            </Field>
          )}
        </div>
      )}
      {form.kind === "upstream" && (
        <Field label="Upstreams" hint="Space-separated; several are load-balanced.">
          <Input
            placeholder="http://10.0.0.5:8080"
            spellCheck={false}
            value={form.upstreams}
            onChange={(event) => set({ upstreams: event.target.value })}
          />
        </Field>
      )}
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
          <Field label="Certificate name" hint="edge/certs/external/<name>/">
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
          <Checkbox checked={form.enabled} onCheckedChange={(checked) => set({ enabled: checked === true })} />
          Enabled
        </label>
        {form.kind !== "redirect" && (
          <label className="check">
            <Checkbox
              checked={route.redirectHttps}
              disabled={route.tls === "none"}
              onCheckedChange={(checked) => setRoute({ redirectHttps: checked === true })}
            />
            HTTPS redirect
          </label>
        )}
        {!compact && (
          <label className="check">
            <Checkbox
              checked={route.accessLog}
              onCheckedChange={(checked) => setRoute({ accessLog: checked === true })}
            />
            Access log
          </label>
        )}
        {!compact && form.kind !== "redirect" && (
          <label
            className="check"
            title={
              form.kind === "app"
                ? "Cache static file responses (css, js, images, fonts) at the edge for 10 minutes. Only enable if those URLs never return per-user content."
                : "Cache public static files (css, js, images, fonts) at the edge. Responses the upstream marks private, no-store, or that set cookies are never cached."
            }
          >
            <Checkbox
              checked={route.staticCache ?? false}
              onCheckedChange={(checked) => setRoute({ staticCache: checked === true })}
            />
            Edge static cache
          </label>
        )}
      </div>
      {save.error && <Alert variant="destructive">{messageOf(save.error)}</Alert>}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!form.name.trim() || !targetSet || save.isPending}>
          {host ? "Save" : compact ? "Add domain" : "Add host"}
        </Button>
      </DialogFooter>
    </form>
  );
}
