import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { Cell, Field } from "../../components/DomainState.tsx";
import { useTrackOperation } from "../operations/OperationTracker.tsx";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { ArgvEditor } from "./CreateApplicationPage.tsx";
import { EnvEditor, useEnvDraft } from "./EnvEditor.tsx";
import { PHPPerformance, phpPerformanceValid } from "./PHPPerformance.tsx";
import { useCatalog } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { NativeSelect } from "@/components/ui/native-select";
import { Globe, Plus } from "lucide-react";
import { Link } from "wouter";
import { HostDialogBody } from "../routing/HostDialog.tsx";
import { pick, sliceKey } from "../../lib/savedSlice.ts";

// Each cell saves only its own fields, so PHP settings are overlaid on the saved runtime key by key.
const phpRuntimeKeys: (keyof T.PHPRuntime)[] = [
  "version",
  "routing",
  "documentRoot",
  "releaseSymlink",
  "strictFileAccess",
];
const phpPerformanceKeys: (keyof T.PHPRuntime)[] = [
  "mode",
  "maxWorkers",
  "webMemoryLimitMb",
  "cliMemoryLimitMb",
  "maxExecutionSeconds",
  "maxInputVars",
  "uploadLimitMb",
];

const defaultPhp: T.PHPRuntime = {
  version: "8.4",
  documentRoot: "public",
  routing: "front-controller",
  mode: "standard",
  uploadLimitMb: 64,
};
const defaultHttp: T.HTTPRuntime = {
  toolchain: "node",
  version: "24",
  argv: ["node", "server.js"],
  workdir: "",
  port: 3000,
};

function phpWith(base: T.PHPRuntime, draft: T.PHPRuntime, keys: (keyof T.PHPRuntime)[]): T.PHPRuntime {
  const out: Record<string, unknown> = { ...base };
  for (const key of keys) out[key] = draft[key];
  return out as unknown as T.PHPRuntime;
}

/** Save one cell: sends only the fields `body` returns, guarded by the generation this page last saw. */
function useCellSave(
  app: T.App,
  generation: number,
  setGeneration: (generation: number) => void,
  body: () => T.UpdateAppRequest,
) {
  const track = useTrackOperation();
  return useMutation({
    mutationFn: () => api.apps.update(app.id, { ...body(), expectedGeneration: generation }),
    onSuccess: (accepted) => {
      setGeneration(accepted.app?.configGeneration ?? generation);
      track(accepted);
    },
  });
}

type CellSave = ReturnType<typeof useCellSave>;

/** Header action of a form cell: a small Save that submits only that cell. */
function SaveButton({ save, disabled, title }: { save: CellSave; disabled?: boolean; title?: string }) {
  return (
    <Button type="submit" size="xs" title={title} disabled={save.isPending || disabled}>
      {save.isPending ? "Saving" : "Save"}
    </Button>
  );
}

function SaveError({ save }: { save: CellSave }) {
  return save.error ? <p className="note note--bad mt-3">{messageOf(save.error)}</p> : null;
}

/** What each filter refuses; blocked requests get a plain 404, never a 403. */
const PROTECTION_HINTS: Record<keyof T.Protection, string> = {
  blockDotFiles: "404 for .env, .git, .htaccess and every dot path except /.well-known/. App and edge Nginx.",
  blockPhpProbes:
    "404 for WordPress paths, phpMyAdmin and webshell names, PHP source extensions and PHP under uploads, cache or vendor. Turn off for WordPress or legacy PHP sites. App and edge Nginx.",
  blockSensitiveFiles:
    "404 for backups, dumps, swap files, archives, source maps and package manager files. App and edge Nginx.",
};

/** Edit an app. The slug and runtime kind are fixed per app incarnation. */
export function ApplicationEditor({ app }: { app: T.App }) {
  const catalog = useCatalog();
  const active = useActiveOperations(app.id);
  const [savedGeneration, setGeneration] = useState(app.configGeneration);
  // The newer of what our last save returned and what the app query now shows:
  // the app also changes elsewhere (another tab, the CLI, an added binding).
  const generation = Math.max(savedGeneration, app.configGeneration);
  const kind = app.runtime.kind;
  const [php, setPhp] = useState<T.PHPRuntime>(app.runtime.php ?? defaultPhp);
  const [http, setHttp] = useState<T.HTTPRuntime>(app.runtime.http ?? defaultHttp);
  const [ingress, setIngress] = useState<T.IngressMode>(app.ingress);
  const [accessLog, setAccessLog] = useState(app.accessLog);
  const [protection, setProtection] = useState<T.Protection>(app.protection);
  const [valkey, setValkey] = useState(app.valkey);
  const [minicrond, setMinicrond] = useState(app.minicrond);
  const [hostDialog, setHostDialog] = useState(false);
  const [resources, setResources] = useState<T.Resources>(app.resources);
  const env = useEnvDraft(app.env);

  // Each cell's draft follows the server when that cell's saved value changes (set during render, no effect).
  // A new editable field must be added to its slice's key and its re-seed.
  const savedRuntimePhp = app.runtime.php ?? defaultPhp;
  const runtimeSaved = sliceKey(
    kind === "php-fpm" ? pick(savedRuntimePhp, phpRuntimeKeys) : (app.runtime.http ?? defaultHttp),
  );
  const [runtimeSeen, setRuntimeSeen] = useState(runtimeSaved);
  if (runtimeSaved !== runtimeSeen) {
    setRuntimeSeen(runtimeSaved);
    if (kind === "php-fpm") setPhp((cur) => phpWith(cur, savedRuntimePhp, phpRuntimeKeys));
    else setHttp(app.runtime.http ?? defaultHttp);
  }
  const performanceSaved = sliceKey(pick(savedRuntimePhp, phpPerformanceKeys));
  const [performanceSeen, setPerformanceSeen] = useState(performanceSaved);
  if (performanceSaved !== performanceSeen) {
    setPerformanceSeen(performanceSaved);
    if (kind === "php-fpm") setPhp((cur) => phpWith(cur, savedRuntimePhp, phpPerformanceKeys));
  }
  const ingressSaved = sliceKey({ ingress: app.ingress, accessLog: app.accessLog, protection: app.protection });
  const [ingressSeen, setIngressSeen] = useState(ingressSaved);
  if (ingressSaved !== ingressSeen) {
    setIngressSeen(ingressSaved);
    setIngress(app.ingress);
    setAccessLog(app.accessLog);
    setProtection(app.protection);
  }
  const resourcesSaved = sliceKey({ resources: app.resources, valkey: app.valkey, minicrond: app.minicrond });
  const [resourcesSeen, setResourcesSeen] = useState(resourcesSaved);
  if (resourcesSaved !== resourcesSeen) {
    setResourcesSeen(resourcesSaved);
    setResources(app.resources);
    setValkey(app.valkey);
    setMinicrond(app.minicrond);
  }
  const envSaved = sliceKey(app.env);
  const [envSeen, setEnvSeen] = useState(envSaved);
  if (envSaved !== envSeen) {
    setEnvSeen(envSaved);
    env.reset(app.env);
  }

  const argvValid = kind !== "http-process" || (http.argv.length > 0 && http.argv.every((arg) => arg.trim() !== ""));
  const phpValid = kind !== "php-fpm" || phpPerformanceValid(php);
  const savedPhp = app.runtime.php ?? php;

  const saveRuntime = useCellSave(app, generation, setGeneration, () => ({
    runtime: kind === "php-fpm" ? { kind, php: phpWith(savedPhp, php, phpRuntimeKeys) } : { kind, http },
  }));
  const savePerformance = useCellSave(app, generation, setGeneration, () => ({
    runtime: { kind, php: phpWith(savedPhp, php, phpPerformanceKeys) },
  }));
  const saveIngress = useCellSave(app, generation, setGeneration, () => ({ ingress, accessLog, protection }));
  const saveResources = useCellSave(app, generation, setGeneration, () => ({ resources, valkey, minicrond }));
  const saveEnv = useCellSave(app, generation, setGeneration, () => ({ env: env.value() }));

  const submit =
    (save: CellSave, valid = true) =>
    (event: FormEvent) => {
      event.preventDefault();
      if (valid) save.mutate();
    };

  const toolchains = catalog.data?.toolchains ?? {};
  const managed = ingress === "managed";
  return (
    <>
      <div>
        <div className="box">
          <Cell
            title={kind === "php-fpm" ? "PHP runtime settings" : "App runtime settings"}
            className="cell--wide"
            onSubmit={submit(saveRuntime, argvValid)}
            action={
              <SaveButton save={saveRuntime} disabled={active.active || !argvValid} title="Running apps may restart." />
            }
          >
            {kind === "php-fpm" ? (
              <div className="grid-3">
                <Field label="Version">
                  <NativeSelect
                    className="w-full"
                    value={php.version}
                    onChange={(e) => setPhp({ ...php, version: e.target.value })}
                  >
                    {(catalog.data?.phpVersions ?? [php.version]).map((v) => (
                      <option key={v}>{v}</option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Routing">
                  <NativeSelect
                    className="w-full"
                    value={php.routing}
                    onChange={(e) => setPhp({ ...php, routing: e.target.value })}
                  >
                    <option value="front-controller">Front controller</option>
                    <option value="legacy">Legacy .php files</option>
                  </NativeSelect>
                </Field>
                <Field label="Document root">
                  <Input value={php.documentRoot} onChange={(e) => setPhp({ ...php, documentRoot: e.target.value })} />
                </Field>
                <Field label="Release symlink">
                  <Input
                    placeholder="optional"
                    value={php.releaseSymlink ?? ""}
                    onChange={(e) => setPhp({ ...php, releaseSymlink: e.target.value || undefined })}
                  />
                </Field>
                <Field
                  label="File access"
                  hint={
                    php.strictFileAccess
                      ? "Sets open_basedir. PHP's path cache turns off: about 40% more CPU per page."
                      : undefined
                  }
                >
                  <NativeSelect
                    className="w-full"
                    value={php.strictFileAccess ? "home" : "container"}
                    onChange={(e) => setPhp({ ...php, strictFileAccess: e.target.value === "home" || undefined })}
                  >
                    <option value="container">Whole container</option>
                    <option value="home">App home only</option>
                  </NativeSelect>
                </Field>
              </div>
            ) : (
              <div className="grid gap-4">
                <div className="grid-3">
                  <Field label="Toolchain">
                    <NativeSelect
                      className="w-full"
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
                    <NativeSelect
                      className="w-full"
                      value={http.version}
                      onChange={(e) => setHttp({ ...http, version: e.target.value })}
                    >
                      {(toolchains[http.toolchain] ?? [http.version]).map((v) => (
                        <option key={v}>{v}</option>
                      ))}
                    </NativeSelect>
                  </Field>
                  <Field label="Port">
                    <Input
                      type="number"
                      value={http.port}
                      onChange={(e) => setHttp({ ...http, port: Number(e.target.value) })}
                    />
                  </Field>
                  <Field label="Working dir">
                    <Input value={http.workdir} onChange={(e) => setHttp({ ...http, workdir: e.target.value })} />
                  </Field>
                  <Field label="Ready path">
                    <Input
                      value={http.readyPath ?? ""}
                      placeholder="/"
                      onChange={(e) => setHttp({ ...http, readyPath: e.target.value || undefined })}
                    />
                  </Field>
                </div>
                <ArgvEditor value={http.argv} onChange={(argv) => setHttp({ ...http, argv })} />
              </div>
            )}
            <SaveError save={saveRuntime} />
          </Cell>
          {kind === "php-fpm" && (
            <Cell
              title="PHP performance settings"
              className="cell--wide"
              onSubmit={submit(savePerformance, phpValid)}
              action={
                <SaveButton
                  save={savePerformance}
                  disabled={active.active || !phpValid}
                  title="Running apps may restart."
                />
              }
            >
              <PHPPerformance php={php} onChange={setPhp} resources={resources} catalog={catalog.data} />
              <SaveError save={savePerformance} />
            </Cell>
          )}
        </div>
        <div className="box box--3">
          <Cell
            title="Ingress settings"
            onSubmit={submit(saveIngress)}
            action={<SaveButton save={saveIngress} disabled={active.active} />}
          >
            <div className="grid gap-4">
              <Field label="Ingress">
                <NativeSelect
                  className="w-full"
                  value={ingress}
                  onChange={(e) => setIngress(e.target.value as T.IngressMode)}
                >
                  <option value="managed">Managed</option>
                  <option value="external">External</option>
                  <option value="none">Private</option>
                </NativeSelect>
              </Field>
              <label className="check" title="The app's own Nginx access log. Edge access logs are set per host.">
                <Checkbox checked={accessLog} onCheckedChange={(checked) => setAccessLog(checked === true)} />
                App access log
              </label>
              <label className="check" title={PROTECTION_HINTS.blockDotFiles}>
                <Checkbox
                  checked={protection.blockDotFiles}
                  onCheckedChange={(checked) => setProtection({ ...protection, blockDotFiles: checked === true })}
                />
                Block dot files
              </label>
              <label className="check" title={PROTECTION_HINTS.blockPhpProbes}>
                <Checkbox
                  checked={protection.blockPhpProbes}
                  onCheckedChange={(checked) => setProtection({ ...protection, blockPhpProbes: checked === true })}
                />
                Block PHP probes
              </label>
              <label className="check" title={PROTECTION_HINTS.blockSensitiveFiles}>
                <Checkbox
                  checked={protection.blockSensitiveFiles}
                  onCheckedChange={(checked) => setProtection({ ...protection, blockSensitiveFiles: checked === true })}
                />
                Block sensitive files
              </label>
              {protection.blockPhpProbes && kind === "php-fpm" && php.routing === "legacy" && (
                <p className="note note--caution">Legacy PHP sites may need PHP probes off.</p>
              )}
            </div>
            <SaveError save={saveIngress} />
          </Cell>
          <Cell title="Hosts" icon={<Globe />} action={<Link href="/ingress">Ingress</Link>}>
            <div className="grid gap-3">
              {app.hosts.length ? (
                <span className="chips">
                  {app.hosts.map((host) => (
                    <span key={host} className="chip">
                      {host}
                    </span>
                  ))}
                </span>
              ) : (
                <p className="note">No hosts</p>
              )}
              {managed ? (
                app.hosts.length === 0 && <p className="note note--bad">Publish needs a host.</p>
              ) : (
                <p className="note">Hosts serve this app only with managed ingress.</p>
              )}
              <div>
                <Button type="button" variant="outline" disabled={!managed} onClick={() => setHostDialog(true)}>
                  <Plus /> Add host
                </Button>
              </div>
            </div>
          </Cell>
          <Cell
            title="Container settings"
            onSubmit={submit(saveResources)}
            action={<SaveButton save={saveResources} disabled={active.active} title="Running apps may restart." />}
          >
            <div className="grid gap-4">
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
              <Field label="Processes">
                <Input
                  type="number"
                  value={resources.pids}
                  onChange={(e) => setResources({ ...resources, pids: Number(e.target.value) })}
                />
              </Field>
              <div className="grid">
                <label
                  className="check"
                  title="Redis-compatible Valkey on 127.0.0.1:6379, no password, snapshots in the app home."
                >
                  <Checkbox checked={valkey} onCheckedChange={(checked) => setValkey(checked === true)} />
                  Valkey (Redis alternative)
                </label>
                <label className="check" title="Minicrond runs cron jobs, workers and Bento's internal tasks.">
                  <Checkbox checked={minicrond} onCheckedChange={(checked) => setMinicrond(checked === true)} />
                  Processes &amp; Tasks (minicrond)
                </label>
              </div>
            </div>
            <SaveError save={saveResources} />
          </Cell>
        </div>
        <div className="box">
          <Cell
            title="Environment"
            className="cell--wide"
            onSubmit={submit(saveEnv)}
            action={<SaveButton save={saveEnv} disabled={active.active} title="Running apps may restart." />}
          >
            <EnvEditor state={env} />
            <SaveError save={saveEnv} />
          </Cell>
        </div>
      </div>
      <Dialog open={hostDialog} onOpenChange={setHostDialog}>
        <DialogContent>
          {hostDialog && <HostDialogBody app={app.slug} compact onDone={() => setHostDialog(false)} />}
        </DialogContent>
      </Dialog>
    </>
  );
}
