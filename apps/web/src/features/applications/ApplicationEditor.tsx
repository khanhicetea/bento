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

// Each cell saves only its own fields, so PHP settings are overlaid on the saved runtime key by key.
const phpRuntimeKeys: (keyof T.PHPRuntime)[] = ["version", "routing", "documentRoot", "releaseSymlink"];
const phpPerformanceKeys: (keyof T.PHPRuntime)[] = [
  "mode",
  "maxWorkers",
  "webMemoryLimitMb",
  "cliMemoryLimitMb",
  "maxExecutionSeconds",
  "maxInputVars",
  "uploadLimitMb",
];

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

/** Edit an app. The slug and runtime kind are fixed per app incarnation. */
export function ApplicationEditor({ app }: { app: T.App }) {
  const catalog = useCatalog();
  const active = useActiveOperations(app.id);
  const [generation, setGeneration] = useState(app.configGeneration);
  const kind = app.runtime.kind;
  const [php, setPhp] = useState<T.PHPRuntime>(
    app.runtime.php ?? {
      version: "8.4",
      documentRoot: "public",
      routing: "front-controller",
      mode: "standard",
      uploadLimitMb: 64,
    },
  );
  const [http, setHttp] = useState<T.HTTPRuntime>(
    app.runtime.http ?? { toolchain: "node", version: "24", argv: ["node", "server.js"], workdir: "", port: 3000 },
  );
  const [ingress, setIngress] = useState<T.IngressMode>(app.ingress);
  const [accessLog, setAccessLog] = useState(app.accessLog);
  const [hostDialog, setHostDialog] = useState(false);
  const [resources, setResources] = useState<T.Resources>(app.resources);
  const env = useEnvDraft(app.env);

  const argvValid = kind !== "http-process" || (http.argv.length > 0 && http.argv.every((arg) => arg.trim() !== ""));
  const phpValid = kind !== "php-fpm" || phpPerformanceValid(php);
  const savedPhp = app.runtime.php ?? php;

  const saveRuntime = useCellSave(app, generation, setGeneration, () => ({
    runtime: kind === "php-fpm" ? { kind, php: phpWith(savedPhp, php, phpRuntimeKeys) } : { kind, http },
  }));
  const savePerformance = useCellSave(app, generation, setGeneration, () => ({
    runtime: { kind, php: phpWith(savedPhp, php, phpPerformanceKeys) },
  }));
  const saveIngress = useCellSave(app, generation, setGeneration, () => ({ ingress, accessLog }));
  const saveResources = useCellSave(app, generation, setGeneration, () => ({ resources }));
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
