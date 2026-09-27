import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Field } from "../../components/DomainState.tsx";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { useCatalog, useOperationMutation } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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

type Props = { app: T.App | null; onClose: () => void; embedded?: boolean };

/** Create or edit an app. The runtime kind is fixed per app incarnation. */
export function ApplicationEditor({ app, onClose, embedded = false }: Props) {
  const catalog = useCatalog();
  const active = useActiveOperations(app?.id);
  const editing = app !== null;
  const [slug, setSlug] = useState(app?.slug ?? "");
  const [expectedGeneration] = useState(app?.configGeneration);
  const [kind, setKind] = useState<T.RuntimeKind>(app?.runtime.kind ?? "php-fpm");
  const [php, setPhp] = useState<T.PHPRuntime>(
    app?.runtime.php ?? {
      version: "8.4",
      documentRoot: "public",
      routing: "front-controller",
      pool: "small",
      uploadLimitMb: 64,
    },
  );
  const [http, setHttp] = useState<T.HTTPRuntime>(
    app?.runtime.http ?? { toolchain: "node", version: "24", argv: ["node", "server.js"], workdir: "", port: 3000 },
  );
  const [argvText, setArgvText] = useState(JSON.stringify(http.argv));
  const [domains, setDomains] = useState((app?.domains ?? []).map((d) => d.name).join("\n"));
  const [ingress, setIngress] = useState<T.IngressMode>(app?.ingress ?? "managed");
  const [route, setRoute] = useState<T.Route>(app?.route ?? { tls: "none", redirectHttps: false, accessLog: false });
  const [resources, setResources] = useState<T.Resources>(
    app?.resources ?? { memoryMb: 512, cpuMillis: 1000, pids: 256 },
  );
  const [binding, setBinding] = useState("sqlite");
  const [argvError, setArgvError] = useState<string | null>(null);

  const save = useOperationMutation(async () => {
    const runtime: T.RuntimeSpec =
      kind === "php-fpm" ? { kind, php } : { kind, http: { ...http, argv: JSON.parse(argvText) as string[] } };
    const domainList = domains.split(/[\s,]+/).filter(Boolean);
    if (editing) {
      return api.apps.update(app.id, {
        expectedGeneration: expectedGeneration ?? app.configGeneration,
        runtime,
        resources,
        ingress,
        domains: domainList,
        route,
      });
    }
    const [engine, service] = binding.split(":");
    return api.apps.create({
      slug,
      runtime,
      resources,
      ingress,
      domains: domainList,
      route,
      bindings: binding === "none" ? [] : [{ engine: engine as T.Engine, service }],
    });
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    if (kind === "http-process") {
      try {
        const parsed: unknown = JSON.parse(argvText);
        if (!Array.isArray(parsed) || parsed.length === 0 || !parsed.every((a) => typeof a === "string"))
          throw new Error();
      } catch {
        setArgvError('argv must be a JSON array of strings, for example ["node","server.js"]');
        return;
      }
    }
    setArgvError(null);
    save.mutate(undefined, { onSuccess: onClose });
  }

  const toolchains = catalog.data?.toolchains ?? {};
  const editor = (
    <form onSubmit={submit} className="bento-form grid gap-5">
      <DialogHeader>
        {embedded ? (
          <>
            <h2 className="text-lg leading-none font-semibold">{editing ? `Edit ${app.slug}` : "New application"}</h2>
            <p className="text-sm text-muted-foreground">
              {editing
                ? "Boot-affecting changes recreate a running instance; frontend and scheduler changes reload in place. Stopped apps stay stopped."
                : "The app is provisioned stopped and unpublished. Start it, verify it, then publish."}
            </p>
          </>
        ) : (
          <>
            <DialogTitle>{editing ? `Edit ${app.slug}` : "New application"}</DialogTitle>
            <DialogDescription>
              {editing
                ? "Boot-affecting changes recreate a running instance; frontend and scheduler changes reload in place. Stopped apps stay stopped."
                : "The app is provisioned stopped and unpublished. Start it, verify it, then publish."}
            </DialogDescription>
          </>
        )}
      </DialogHeader>
      <section className="bento-form-section">
        <div className="bento-form-section__heading">
          <h3>Identity</h3>
          <p>The app’s permanent name and runtime type.</p>
        </div>
        <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
          <Field label="Slug" hint="Permanent; names the home /home/<slug>.">
            <Input value={slug} disabled={editing} onChange={(e) => setSlug(e.target.value)} required />
          </Field>
          <Field label="Runtime">
            <NativeSelect value={kind} disabled={editing} onChange={(e) => setKind(e.target.value as T.RuntimeKind)}>
              <option value="php-fpm">PHP-FPM (local Nginx)</option>
              <option value="http-process">HTTP process</option>
            </NativeSelect>
          </Field>
        </div>
      </section>
      <section className="bento-form-section">
        <div className="bento-form-section__heading">
          <h3>Runtime</h3>
          <p>How the application starts and serves requests.</p>
        </div>
        {kind === "php-fpm" ? (
          <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
            <Field label="PHP version">
              <NativeSelect value={php.version} onChange={(e) => setPhp({ ...php, version: e.target.value })}>
                {(catalog.data?.phpVersions ?? [php.version]).map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="Document root" hint="Relative to /home/&lt;slug&gt;/app.">
              <Input value={php.documentRoot} onChange={(e) => setPhp({ ...php, documentRoot: e.target.value })} />
            </Field>
            <Field label="Routing">
              <NativeSelect value={php.routing} onChange={(e) => setPhp({ ...php, routing: e.target.value })}>
                <option value="front-controller">Front controller (index.php only)</option>
                <option value="legacy">Legacy (any .php under the root)</option>
              </NativeSelect>
            </Field>
            <Field label="Pool profile">
              <NativeSelect value={php.pool} onChange={(e) => setPhp({ ...php, pool: e.target.value })}>
                {(catalog.data?.poolProfiles ?? [php.pool]).map((p) => (
                  <option key={p}>{p}</option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="Upload limit (MB)">
              <Input
                type="number"
                value={php.uploadLimitMb}
                onChange={(e) => setPhp({ ...php, uploadLimitMb: Number(e.target.value) })}
              />
            </Field>
            <Field label="Release symlink" hint="Optional, e.g. current (deliberately traversed).">
              <Input
                value={php.releaseSymlink ?? ""}
                onChange={(e) => setPhp({ ...php, releaseSymlink: e.target.value || undefined })}
              />
            </Field>
          </div>
        ) : (
          <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
            <Field label="Toolchain">
              <NativeSelect
                value={http.toolchain}
                onChange={(e) =>
                  setHttp({
                    ...http,
                    toolchain: e.target.value,
                    version: (toolchains[e.target.value] ?? [""]).at(-1) ?? "",
                  })
                }
              >
                {Object.keys(toolchains).map((t) => (
                  <option key={t}>{t}</option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="Version">
              <NativeSelect value={http.version} onChange={(e) => setHttp({ ...http, version: e.target.value })}>
                {(toolchains[http.toolchain] ?? [http.version]).map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </NativeSelect>
            </Field>
            <Field label="Command argv (JSON)" hint="Executed directly, never through a shell.">
              <Input value={argvText} onChange={(e) => setArgvText(e.target.value)} />
            </Field>
            <Field label="Working directory" hint="Relative to /home/&lt;slug&gt;/app.">
              <Input value={http.workdir} onChange={(e) => setHttp({ ...http, workdir: e.target.value })} />
            </Field>
            <Field label="HTTP port" hint="Listen on 0.0.0.0 inside the container.">
              <Input
                type="number"
                value={http.port}
                onChange={(e) => setHttp({ ...http, port: Number(e.target.value) })}
              />
            </Field>
            <Field label="Readiness path">
              <Input
                value={http.readyPath ?? ""}
                placeholder="/"
                onChange={(e) => setHttp({ ...http, readyPath: e.target.value || undefined })}
              />
            </Field>
          </div>
        )}
      </section>
      <section className="bento-form-section">
        <div className="bento-form-section__heading">
          <h3>Resources</h3>
          <p>Limits for this app’s container.</p>
        </div>
        <div className="grid grid-cols-3 gap-4 max-[640px]:grid-cols-1">
          <Field label="Memory (MB)">
            <Input
              type="number"
              value={resources.memoryMb}
              onChange={(e) => setResources({ ...resources, memoryMb: Number(e.target.value) })}
            />
          </Field>
          <Field label="CPU (millicores)">
            <Input
              type="number"
              value={resources.cpuMillis}
              onChange={(e) => setResources({ ...resources, cpuMillis: Number(e.target.value) })}
            />
          </Field>
          <Field label="Process limit">
            <Input
              type="number"
              value={resources.pids}
              onChange={(e) => setResources({ ...resources, pids: Number(e.target.value) })}
            />
          </Field>
        </div>
      </section>
      <section className="bento-form-section">
        <div className="bento-form-section__heading">
          <h3>Routing</h3>
          <p>Choose who manages the public route and its domains.</p>
        </div>
        <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
          <Field label="Ingress" hint="Only managed routes are controlled by Bento.">
            <NativeSelect value={ingress} onChange={(e) => setIngress(e.target.value as T.IngressMode)}>
              <option value="managed">Managed edge</option>
              <option value="external">External (tunnel or proxy, operator-owned)</option>
              <option value="none">None (private only)</option>
            </NativeSelect>
          </Field>
          <Field label="TLS">
            <NativeSelect value={route.tls} onChange={(e) => setRoute({ ...route, tls: e.target.value as T.TLSMode })}>
              <option value="none">None</option>
              <option value="self-signed">Self-signed boot certificate</option>
              <option value="acme">ACME (Let's Encrypt)</option>
              <option value="external">External certificate</option>
            </NativeSelect>
          </Field>
        </div>
        {route.tls === "external" && (
          <Field label="Certificate name" hint="Files at edge/certs/external/<name>/fullchain.pem and privkey.pem.">
            <Input value={route.certName ?? ""} onChange={(e) => setRoute({ ...route, certName: e.target.value })} />
          </Field>
        )}
        <div className="flex flex-wrap gap-4 text-sm">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={route.redirectHttps}
              disabled={route.tls === "none"}
              onChange={(e) => setRoute({ ...route, redirectHttps: e.target.checked })}
            />
            Redirect HTTP to HTTPS
          </label>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={route.accessLog}
              onChange={(e) => setRoute({ ...route, accessLog: e.target.checked })}
            />
            Access log
          </label>
        </div>
        <Field label="Domains" hint="One per line; the first is primary.">
          <textarea
            className="min-h-20 rounded-md border border-input bg-transparent p-2 text-sm"
            value={domains}
            onChange={(e) => setDomains(e.target.value)}
          />
        </Field>
      </section>
      {!editing && (
        <section className="bento-form-section">
          <div className="bento-form-section__heading">
            <h3>Data</h3>
            <p>The initial binding is add-only.</p>
          </div>
          <InitialBinding value={binding} onChange={setBinding} />
        </section>
      )}
      {(argvError || save.error) && <Alert variant="destructive">{argvError ?? messageOf(save.error)}</Alert>}
      <DialogFooter>
        {!embedded && (
          <Button type="button" variant="outline" onClick={onClose}>
            Cancel
          </Button>
        )}
        <Button type="submit" disabled={save.isPending || (editing && active.active)}>
          {editing ? "Save changes" : "Create application"}
        </Button>
      </DialogFooter>
    </form>
  );
  if (embedded) return <section className="bento-editor-shell rounded-xl border bg-card p-5">{editor}</section>;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[92vh] overflow-y-auto sm:max-w-3xl">{editor}</DialogContent>
    </Dialog>
  );
}

function InitialBinding({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const services = useServicesForBinding();
  return (
    <Field label="Initial data binding" hint="Bindings are add-only; more can be added later.">
      <NativeSelect value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="sqlite">SQLite (private file)</option>
        {services.map((s) => (
          <option key={s.name} value={`${s.engine}:${s.name}`}>
            {s.engine} {s.version} ({s.name})
          </option>
        ))}
        <option value="none">No database</option>
      </NativeSelect>
    </Field>
  );
}

function useServicesForBinding() {
  const q = useQuery({ queryKey: keys.services, queryFn: ({ signal }) => api.services.list(signal) });
  return (q.data?.services ?? []).filter((s) => s.engine === "mysql" || s.engine === "postgres");
}
