import { useState, type FormEvent, type ReactNode } from "react";
import type { Application, ApplicationList, SaveApplicationInput } from "@bento/shared";
import { Database, Globe2, Save, Server, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Alert } from "@/components/ui/alert";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

type ApplicationEditorProps = {
  application: Application | null;
  settings: ApplicationList;
  error: string | null;
  saving: boolean;
  onClose: () => void;
  onSave: (input: SaveApplicationInput) => Promise<Application>;
};

export function ApplicationEditor({ application, settings, error, saving, onClose, onSave }: ApplicationEditorProps) {
  const primaryDatabase = application?.databases[0];
  const initialDatabase = primaryDatabase
    ? databaseSelection(primaryDatabase.engine, primaryDatabase.service)
    : databaseSelection(settings.defaults?.databaseEngine ?? "sqlite", settings.defaults?.databaseService);
  const [database, setDatabase] = useState(initialDatabase);
  const [tls, setTls] = useState(application?.tls ?? "shared");
  const [kind, setKind] = useState<Application["kind"]>(application?.kind ?? "php");
  const relationalDatabase = database.startsWith("mysql:") || database.startsWith("postgres:");
  const creating = application === null;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const [databaseEngine, databaseService] = database.split(":") as [
      SaveApplicationInput["databaseEngine"],
      string | undefined,
    ];
    const databaseName = String(form.get("databaseName") ?? "").trim();
    const input: SaveApplicationInput = {
      slug: String(form.get("slug") ?? "").trim(),
      kind,
      domain: String(form.get("domain") ?? "").trim(),
      aliases: String(form.get("aliases") ?? "")
        .split(",")
        .map((alias) => alias.trim())
        .filter(Boolean),
      ...(kind === "php"
        ? {
            documentRoot: String(form.get("documentRoot") ?? "").trim(),
            entrypointMode: String(form.get("entrypointMode")) as SaveApplicationInput["entrypointMode"],
            phpVersion: String(form.get("phpVersion")),
            fpmProfile: String(form.get("fpmProfile")),
          }
        : {
            processLanguage: String(form.get("processLanguage")) as SaveApplicationInput["processLanguage"],
            processVersion: String(form.get("processVersion") ?? "").trim(),
            processCommand: String(form.get("processCommand") ?? "")
              .split("\n")
              .map((argument) => argument.trim())
              .filter(Boolean),
            processWorkdir: String(form.get("processWorkdir") ?? "").trim() || undefined,
            processPort: Number(form.get("processPort") ?? 8080),
            processHealthPath: String(form.get("processHealthPath") ?? "").trim() || undefined,
          }),
      tls,
      tlsCertificatePath: tls === "external" ? String(form.get("tlsCertificatePath") ?? "").trim() : undefined,
      tlsKeyPath: tls === "external" ? String(form.get("tlsKeyPath") ?? "").trim() : undefined,
      accessLog: form.get("accessLog") === "on",
      databaseEngine,
      databaseService,
      createDatabase: relationalDatabase && form.get("createDatabase") === "on",
      databaseName: relationalDatabase && form.get("createDatabase") === "on" ? databaseName || undefined : undefined,
    };

    try {
      await onSave(input);
      onClose();
    } catch {
      // The TanStack mutation exposes the sanitized oRPC error in the dialog.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent
        className="w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] max-w-[1080px] gap-0 overflow-y-auto p-0 sm:!max-w-[1080px]"
        showCloseButton={!saving}
      >
        <div className="border-b border-border bg-muted/30 px-8 py-6 pr-16 max-[700px]:px-4 max-[700px]:py-5">
          <DialogHeader className="gap-3">
            <div className="flex items-center gap-3">
              <span className="grid size-11 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                <Server className="size-5" aria-hidden="true" />
              </span>
              <div className="min-w-0">
                <DialogTitle className="text-xl">
                  {creating ? "Create application" : `Edit ${application.slug}`}
                </DialogTitle>
                <DialogDescription className="mt-1">
                  Configure identity, runtime, data, and delivery settings in one place.
                </DialogDescription>
              </div>
            </div>
          </DialogHeader>
        </div>

        <form onSubmit={(event) => void submit(event)}>
          <fieldset
            disabled={saving}
            className="space-y-5 px-8 py-6 max-[700px]:space-y-4 max-[700px]:px-4 max-[700px]:py-5"
          >
            {error && <Alert variant="destructive">{error}</Alert>}

            <section className="rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
              <SectionHeading
                icon={<Globe2 className="size-4" />}
                title="Application identity"
                description="The name and domains operators use to reach this application."
              />
              <div className="mt-5 grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                <label>
                  <FieldLabel>Slug</FieldLabel>
                  <Input
                    className="w-full"
                    name="slug"
                    required
                    maxLength={63}
                    pattern="[a-z0-9][a-z0-9-]*"
                    defaultValue={application?.slug ?? ""}
                    readOnly={!creating}
                  />
                  <FieldHint>Lowercase letters, numbers, and hyphens.</FieldHint>
                </label>
                <label>
                  <FieldLabel>Primary domain</FieldLabel>
                  <Input
                    className="w-full"
                    name="domain"
                    required
                    defaultValue={application?.domain ?? ""}
                    placeholder="app.example.com"
                  />
                  <FieldHint>This is the public address for the application.</FieldHint>
                </label>
                <label className="col-span-full">
                  <FieldLabel>
                    Aliases <span className="font-normal text-muted-foreground">(comma-separated)</span>
                  </FieldLabel>
                  <Input
                    className="w-full"
                    name="aliases"
                    defaultValue={application?.aliases.join(", ") ?? ""}
                    placeholder="www.example.com, alternate.example.com"
                  />
                  <FieldHint>Optional domains that should point to the same application.</FieldHint>
                </label>
              </div>
            </section>

            <section className="rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
              <SectionHeading
                icon={<Server className="size-4" />}
                title="Runtime and routing"
                description="Choose a PHP pool or a supervised Node.js, Bun, or Python HTTP process."
              />
              <div className="mt-5 grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                <label className="col-span-full">
                  <FieldLabel>Runtime kind</FieldLabel>
                  <NativeSelect
                    className="w-full"
                    value={kind}
                    disabled={!creating}
                    onChange={(event) => setKind(event.target.value as Application["kind"])}
                  >
                    <NativeSelectOption value="php">PHP-FPM application</NativeSelectOption>
                    <NativeSelectOption value="process">Node.js, Bun, or Python process</NativeSelectOption>
                  </NativeSelect>
                  {!creating && <FieldHint>Runtime kind cannot be changed in place.</FieldHint>}
                </label>
                {kind === "php" ? (
                  <>
                    <label>
                      <FieldLabel>PHP version</FieldLabel>
                      <NativeSelect
                        className="w-full"
                        name="phpVersion"
                        required
                        defaultValue={application?.phpVersion ?? settings.defaults?.phpVersion}
                      >
                        {settings.phpVersions.map((version) => (
                          <NativeSelectOption key={version} value={version}>
                            PHP {version}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </label>
                    <label>
                      <FieldLabel>FPM capacity</FieldLabel>
                      <NativeSelect
                        className="w-full"
                        name="fpmProfile"
                        required
                        defaultValue={application?.fpmProfile ?? settings.defaults?.fpmProfile}
                      >
                        {settings.fpmProfiles.map((profile) => (
                          <NativeSelectOption key={profile} value={profile}>
                            {profile}
                          </NativeSelectOption>
                        ))}
                      </NativeSelect>
                    </label>
                    <label>
                      <FieldLabel>Document root</FieldLabel>
                      <Input
                        className="w-full"
                        name="documentRoot"
                        required
                        defaultValue={application?.documentRoot ?? "public"}
                      />
                      <FieldHint>Directory served by the web runtime.</FieldHint>
                    </label>
                    <label>
                      <FieldLabel>Routing mode</FieldLabel>
                      <NativeSelect
                        className="w-full"
                        name="entrypointMode"
                        defaultValue={application?.entrypointMode ?? "front-controller"}
                      >
                        <NativeSelectOption value="front-controller">Front controller</NativeSelectOption>
                        <NativeSelectOption value="legacy">Direct PHP files (legacy)</NativeSelectOption>
                      </NativeSelect>
                    </label>
                  </>
                ) : (
                  <>
                    <label>
                      <FieldLabel>Toolchain</FieldLabel>
                      <NativeSelect
                        className="w-full"
                        name="processLanguage"
                        defaultValue={application?.processRuntime?.language ?? "node"}
                      >
                        <NativeSelectOption value="node">Node.js</NativeSelectOption>
                        <NativeSelectOption value="bun">Bun</NativeSelectOption>
                        <NativeSelectOption value="python">Python</NativeSelectOption>
                      </NativeSelect>
                    </label>
                    <label>
                      <FieldLabel>Exact runtime version</FieldLabel>
                      <Input
                        className="w-full"
                        name="processVersion"
                        required
                        defaultValue={application?.processRuntime?.version ?? ""}
                        placeholder="24 or 3.13.1"
                      />
                    </label>
                    <label className="col-span-full">
                      <FieldLabel>Start argv</FieldLabel>
                      <Textarea
                        className="min-h-28 w-full font-mono"
                        name="processCommand"
                        required
                        defaultValue={application?.processRuntime?.command.join("\n") ?? ""}
                        placeholder={"node\ndist/server.js"}
                      />
                      <FieldHint>One literal argument per line; no implicit shell.</FieldHint>
                    </label>
                    <label>
                      <FieldLabel>Working directory</FieldLabel>
                      <Input
                        className="w-full"
                        name="processWorkdir"
                        defaultValue={application?.processRuntime?.workdir ?? ""}
                        placeholder="Defaults to /home/&lt;slug&gt;/code"
                      />
                    </label>
                    <label>
                      <FieldLabel>Private HTTP port</FieldLabel>
                      <Input
                        className="w-full"
                        name="processPort"
                        type="number"
                        min={1024}
                        max={65535}
                        required
                        defaultValue={application?.processRuntime?.internalPort ?? 8080}
                      />
                    </label>
                    <label className="col-span-full">
                      <FieldLabel>HTTP health path</FieldLabel>
                      <Input
                        className="w-full"
                        name="processHealthPath"
                        defaultValue={application?.processRuntime?.healthPath ?? ""}
                        placeholder="Optional, for example /health"
                      />
                      <FieldHint>Without a path Bento checks TCP readiness.</FieldHint>
                    </label>
                  </>
                )}
              </div>
            </section>

            {creating && (
              <section className="rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
                <SectionHeading
                  icon={<Database className="size-4" />}
                  title="Initial database binding"
                  description="Create the first storage binding while provisioning the application."
                />
                <div className="mt-5 grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                  <label className="col-span-full">
                    <FieldLabel>Engine or managed service</FieldLabel>
                    <NativeSelect
                      className="w-full"
                      value={database}
                      onChange={(event) => setDatabase(event.target.value)}
                    >
                      {settings.databaseServices.map((service) => (
                        <NativeSelectOption
                          key={`${service.engine}:${service.service}`}
                          value={databaseSelection(service.engine, service.service)}
                        >
                          {service.engine === "mysql" ? "MySQL" : "PostgreSQL"} {service.version} ({service.service})
                        </NativeSelectOption>
                      ))}
                      <NativeSelectOption value="sqlite">SQLite</NativeSelectOption>
                      <NativeSelectOption value="litestream">SQLite + Litestream</NativeSelectOption>
                    </NativeSelect>
                  </label>
                  {relationalDatabase && (
                    <>
                      <label className="flex items-start gap-3 rounded-xl border border-border bg-muted/30 p-3">
                        <Checkbox name="createDatabase" defaultChecked={creating} className="mt-0.5" />
                        <span className="text-sm">
                          <span className="font-medium">Create database</span>
                          <small className="mt-1 block text-xs text-muted-foreground">
                            Existing bindings and durable databases are never removed here.
                          </small>
                        </span>
                      </label>
                      <label>
                        <FieldLabel>Database name</FieldLabel>
                        <Input
                          className="w-full"
                          name="databaseName"
                          defaultValue={creating ? "" : primaryDatabase?.names[0]}
                          placeholder="Defaults to the application slug"
                        />
                      </label>
                    </>
                  )}
                </div>
              </section>
            )}

            <section className="rounded-2xl border border-border bg-card p-5 shadow-sm max-[700px]:p-4">
              <SectionHeading
                icon={<ShieldCheck className="size-4" />}
                title="TLS and logs"
                description="Protect traffic and decide which request information is retained."
              />
              <div className="mt-5 grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
                <label>
                  <FieldLabel>TLS mode</FieldLabel>
                  <NativeSelect
                    className="w-full"
                    value={tls}
                    onChange={(event) => setTls(event.target.value as Application["tls"])}
                  >
                    <NativeSelectOption value="shared">Shared starter certificate</NativeSelectOption>
                    <NativeSelectOption value="self-ca">Stack private CA</NativeSelectOption>
                    <NativeSelectOption value="acme">ACME</NativeSelectOption>
                    <NativeSelectOption value="external">External certificate</NativeSelectOption>
                  </NativeSelect>
                </label>
                <label className="flex cursor-pointer items-center gap-3 rounded-xl border border-border bg-muted/30 px-3 py-2.5">
                  <Checkbox name="accessLog" defaultChecked={application?.accessLog ?? false} />
                  <span className="text-sm font-medium">Enable access logs</span>
                </label>
                {tls === "external" && (
                  <>
                    <label>
                      <FieldLabel>Certificate path</FieldLabel>
                      <Input
                        className="w-full"
                        name="tlsCertificatePath"
                        required
                        defaultValue={application?.tlsCertificatePath ?? ""}
                        placeholder="/etc/ssl/certs/app.crt"
                      />
                    </label>
                    <label>
                      <FieldLabel>Private key path</FieldLabel>
                      <Input
                        className="w-full"
                        name="tlsKeyPath"
                        required
                        defaultValue={application?.tlsKeyPath ?? ""}
                        placeholder="/etc/ssl/private/app.key"
                      />
                    </label>
                  </>
                )}
              </div>
            </section>
          </fieldset>

          <DialogFooter className="border-t border-border bg-muted/30 px-8 py-4 max-[700px]:px-4">
            <Button type="button" variant="ghost" disabled={saving} onClick={onClose}>
              Cancel
            </Button>
            <Button disabled={saving} type="submit">
              {saving ? <Spinner /> : <Save className="size-4" aria-hidden="true" />}
              {creating ? "Create and apply" : "Save and apply"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function SectionHeading({ icon, title, description }: { icon: ReactNode; title: string; description: string }) {
  return (
    <div className="flex items-start gap-3">
      <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-muted text-primary">{icon}</span>
      <div>
        <h3 className="m-0 text-base font-semibold">{title}</h3>
        <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function FieldLabel({ children }: { children: ReactNode }) {
  return <span className="mb-1.5 block text-sm font-medium">{children}</span>;
}

function FieldHint({ children }: { children: ReactNode }) {
  return <small className="mt-1.5 block text-xs text-muted-foreground">{children}</small>;
}

function databaseSelection(engine: string, service?: string): string {
  return service ? `${engine}:${service}` : engine;
}
