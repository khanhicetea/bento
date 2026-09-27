import { useState, type FormEvent, type KeyboardEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, ChevronLeft, ChevronRight, Plus, X } from "lucide-react";
import { useLocation } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Field, Page, PageHeader } from "../../components/DomainState.tsx";
import { useCatalog, useOperationMutation } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

const reservedSlugs = new Set([
  "root",
  "bento",
  "admin",
  "www-data",
  "nginx",
  "edge",
  "mysql",
  "postgres",
  "redis",
  "daemon",
  "nobody",
  "system",
  "cloudflared",
  "backup",
  "tool",
]);
const stepLabels = ["Identity & runtime", "Runtime details", "Routing & data", "Review"];

function slugError(slug: string): string | null {
  if (!/^[a-z][a-z0-9-]{1,30}[a-z0-9]$/.test(slug) || slug.includes("--"))
    return "Use 3–32 lowercase letters, digits, or single hyphens, starting with a letter.";
  if (reservedSlugs.has(slug)) return `“${slug}” is reserved.`;
  return null;
}
function validDomain(domain: string): boolean {
  const value = domain.toLowerCase().replace(/\.$/, "");
  return (
    value.length >= 3 &&
    value.length <= 253 &&
    value.includes(".") &&
    !/^\d+(?:\.\d+)+$/.test(value) &&
    value.split(".").every((part) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(part))
  );
}
function validRelativePath(value: string): boolean {
  return (
    value === "" || (!value.startsWith("/") && value.split("/").every((part) => part !== ".." && !part.startsWith(".")))
  );
}

export function CreateApplicationPage() {
  const [, navigate] = useLocation();
  const catalog = useCatalog();
  const services = useQuery({ queryKey: keys.services, queryFn: ({ signal }) => api.services.list(signal) });
  const [step, setStep] = useState(0);
  const [slug, setSlug] = useState("");
  const [kind, setKind] = useState<T.RuntimeKind>("php-fpm");
  const [php, setPhp] = useState<T.PHPRuntime>({
    version: "8.4",
    documentRoot: "public",
    routing: "front-controller",
    pool: "small",
    uploadLimitMb: 64,
  });
  const [http, setHttp] = useState<T.HTTPRuntime>({
    toolchain: "node",
    version: "24",
    argv: ["node", "server.js"],
    workdir: "",
    port: 3000,
  });
  const [ingress, setIngress] = useState<T.IngressMode>("managed");
  const [route, setRoute] = useState<T.Route>({ tls: "none", redirectHttps: false, accessLog: false });
  const [domains, setDomains] = useState<string[]>([]);
  const [domainDraft, setDomainDraft] = useState("");
  const [domainError, setDomainError] = useState("");
  const [binding, setBinding] = useState("sqlite");
  const [resources, setResources] = useState<T.Resources>({ memoryMb: 512, cpuMillis: 1000, pids: 256 });
  const [showAdvanced, setShowAdvanced] = useState(false);

  const create = useOperationMutation(() => {
    const [engine, service] = binding.split(":");
    const runtime: T.RuntimeSpec = kind === "php-fpm" ? { kind, php } : { kind, http };
    return api.apps.create({
      slug,
      runtime,
      resources,
      ingress,
      domains,
      route,
      bindings: binding === "none" ? [] : [{ engine: engine as T.Engine, service }],
    });
  });

  const identityValid = slug.length > 0 && slugError(slug) === null;
  const detailsValid =
    kind === "php-fpm"
      ? php.version !== "" &&
        php.uploadLimitMb >= 1 &&
        php.uploadLimitMb <= 4096 &&
        validRelativePath(php.documentRoot) &&
        validRelativePath(php.releaseSymlink ?? "")
      : http.toolchain !== "" &&
        http.version !== "" &&
        http.argv.length > 0 &&
        http.argv.length <= 64 &&
        http.argv.every((arg) => arg.trim() !== "") &&
        http.port >= 1024 &&
        http.port <= 65535 &&
        validRelativePath(http.workdir) &&
        (!http.readyPath || http.readyPath.startsWith("/"));
  const routingValid =
    (ingress !== "managed" || domains.length > 0) &&
    (route.tls !== "external" || /^[a-z0-9][a-z0-9._-]{0,63}$/.test(route.certName ?? "")) &&
    resources.memoryMb >= 64 &&
    resources.memoryMb <= 262144 &&
    resources.cpuMillis >= 50 &&
    resources.cpuMillis <= 256000 &&
    resources.pids >= 32 &&
    resources.pids <= 65536;
  const currentValid = step === 0 ? identityValid : step === 1 ? detailsValid : step === 2 ? routingValid : true;

  function addDomain() {
    const value = domainDraft.trim().toLowerCase().replace(/\.$/, "");
    if (!value) return;
    if (!validDomain(value)) {
      setDomainError("Enter a valid DNS hostname such as app.example.com.");
      return;
    }
    if (!domains.includes(value)) setDomains((current) => [...current, value]);
    setDomainDraft("");
    setDomainError("");
  }
  function domainKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter" || event.key === ",") {
      event.preventDefault();
      addDomain();
    }
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (step < 3) {
      if (currentValid) setStep((value) => value + 1);
      return;
    }
    create.mutate(undefined, { onSuccess: () => navigate("/apps") });
  }

  const toolchains = catalog.data?.toolchains ?? {};
  return (
    <Page>
      <PageHeader
        title="Create application"
        description="Created stopped and unpublished. Next: start, verify, then publish."
      />
      <ol className="mb-6 grid grid-cols-2 gap-2 p-0 sm:grid-cols-4" aria-label="Creation progress">
        {stepLabels.map((label, index) => (
          <li
            key={label}
            className={`flex items-center gap-2 rounded-lg border p-3 text-xs ${index === step ? "border-primary bg-primary/5 font-semibold" : index < step ? "text-success" : "text-muted-foreground"}`}
            aria-current={index === step ? "step" : undefined}
          >
            <span className="grid size-5 shrink-0 place-items-center rounded-full border">
              {index < step ? <Check className="size-3" /> : index + 1}
            </span>
            {label}
          </li>
        ))}
      </ol>
      <form onSubmit={submit} className="rounded-xl border bg-card p-5 shadow-sm">
        {step === 0 && (
          <section className="grid gap-5">
            <div>
              <h2 className="m-0 text-lg font-semibold">Identity & runtime</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                The slug and runtime kind are permanent for this app incarnation.
              </p>
            </div>
            <Field
              label="Slug"
              hint={slug ? `Home: /home/${slug}` : "3–32 lowercase letters, digits, or single hyphens."}
            >
              <Input
                autoFocus
                value={slug}
                aria-invalid={slug.length > 0 && !!slugError(slug)}
                onChange={(event) => setSlug(event.target.value.toLowerCase())}
              />
            </Field>
            {slug.length > 0 && slugError(slug) && <Alert variant="destructive">{slugError(slug)}</Alert>}
            <fieldset className="grid gap-3 sm:grid-cols-2">
              <legend className="mb-2 text-sm font-medium">Runtime</legend>
              {(
                [
                  ["php-fpm", "PHP-FPM", "PHP with local Nginx and managed pool profiles."],
                  ["http-process", "HTTP process", "A direct argv process listening on an internal port."],
                ] as const
              ).map(([value, title, body]) => (
                <label
                  key={value}
                  className={`cursor-pointer rounded-lg border p-4 ${kind === value ? "border-primary bg-primary/5" : ""}`}
                >
                  <input
                    className="sr-only"
                    type="radio"
                    name="runtime"
                    value={value}
                    checked={kind === value}
                    onChange={() => setKind(value)}
                  />
                  <strong>{title}</strong>
                  <span className="mt-1 block text-xs text-muted-foreground">{body}</span>
                </label>
              ))}
            </fieldset>
          </section>
        )}

        {step === 1 && (
          <section className="grid gap-5">
            <div>
              <h2 className="m-0 text-lg font-semibold">Runtime details</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Configure how the application starts and becomes ready.
              </p>
            </div>
            {kind === "php-fpm" ? (
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="PHP version">
                  <NativeSelect
                    value={php.version}
                    onChange={(event) => setPhp({ ...php, version: event.target.value })}
                  >
                    {(catalog.data?.phpVersions ?? [php.version]).map((version) => (
                      <option key={version}>{version}</option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Document root">
                  <Input
                    value={php.documentRoot}
                    onChange={(event) => setPhp({ ...php, documentRoot: event.target.value })}
                  />
                </Field>
                <Field label="Routing">
                  <NativeSelect
                    value={php.routing}
                    onChange={(event) => setPhp({ ...php, routing: event.target.value })}
                  >
                    <option value="front-controller">Front controller</option>
                    <option value="legacy">Legacy PHP files</option>
                  </NativeSelect>
                </Field>
                <Field label="Pool profile">
                  <NativeSelect value={php.pool} onChange={(event) => setPhp({ ...php, pool: event.target.value })}>
                    {(catalog.data?.poolProfiles ?? [php.pool]).map((pool) => (
                      <option key={pool}>{pool}</option>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label="Upload limit (MB)">
                  <Input
                    type="number"
                    min="1"
                    max="4096"
                    value={php.uploadLimitMb}
                    onChange={(event) => setPhp({ ...php, uploadLimitMb: Number(event.target.value) })}
                  />
                </Field>
                <Field label="Release symlink" hint="Optional relative path, such as current.">
                  <Input
                    value={php.releaseSymlink ?? ""}
                    onChange={(event) => setPhp({ ...php, releaseSymlink: event.target.value || undefined })}
                  />
                </Field>
              </div>
            ) : (
              <div className="grid gap-4">
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="Toolchain">
                    <NativeSelect
                      value={http.toolchain}
                      onChange={(event) => {
                        const toolchain = event.target.value;
                        setHttp({ ...http, toolchain, version: (toolchains[toolchain] ?? [""]).at(-1) ?? "" });
                      }}
                    >
                      {Object.keys(toolchains).map((toolchain) => (
                        <option key={toolchain}>{toolchain}</option>
                      ))}
                    </NativeSelect>
                  </Field>
                  <Field label="Version">
                    <NativeSelect
                      value={http.version}
                      onChange={(event) => setHttp({ ...http, version: event.target.value })}
                    >
                      {(toolchains[http.toolchain] ?? [http.version]).map((version) => (
                        <option key={version}>{version}</option>
                      ))}
                    </NativeSelect>
                  </Field>
                </div>
                <ArgvEditor value={http.argv} onChange={(argv) => setHttp({ ...http, argv })} />
                <div className="rounded-md bg-muted p-3 text-xs">
                  <span className="text-muted-foreground">Preview: </span>
                  <code>{http.argv.join(" ")}</code>
                </div>
                <div className="grid gap-4 sm:grid-cols-3">
                  <Field label="Working directory">
                    <Input
                      value={http.workdir}
                      onChange={(event) => setHttp({ ...http, workdir: event.target.value })}
                    />
                  </Field>
                  <Field label="HTTP port">
                    <Input
                      type="number"
                      min="1024"
                      max="65535"
                      value={http.port}
                      onChange={(event) => setHttp({ ...http, port: Number(event.target.value) })}
                    />
                  </Field>
                  <Field label="Readiness path">
                    <Input
                      placeholder="/"
                      value={http.readyPath ?? ""}
                      onChange={(event) => setHttp({ ...http, readyPath: event.target.value || undefined })}
                    />
                  </Field>
                </div>
              </div>
            )}
          </section>
        )}

        {step === 2 && (
          <section className="grid gap-5">
            <div>
              <h2 className="m-0 text-lg font-semibold">Routing, data & resources</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                Choose route ownership and an initial add-only data binding.
              </p>
            </div>
            <fieldset className="grid gap-3 sm:grid-cols-3">
              <legend className="mb-2 text-sm font-medium">Ingress</legend>
              {(
                [
                  ["managed", "Managed edge", "Bento owns publication, TLS, and route policy."],
                  ["external", "External", "Tunnel or operator proxy owns the route."],
                  ["none", "Private", "No public route is configured."],
                ] as const
              ).map(([value, title, body]) => (
                <label
                  key={value}
                  className={`cursor-pointer rounded-lg border p-3 ${ingress === value ? "border-primary bg-primary/5" : ""}`}
                >
                  <input
                    className="sr-only"
                    type="radio"
                    name="ingress"
                    checked={ingress === value}
                    onChange={() => setIngress(value)}
                  />
                  <strong className="text-sm">{title}</strong>
                  <span className="mt-1 block text-xs text-muted-foreground">{body}</span>
                </label>
              ))}
            </fieldset>
            <DomainsInput
              domains={domains}
              draft={domainDraft}
              error={domainError}
              onDraft={setDomainDraft}
              onKeyDown={domainKeyDown}
              onAdd={addDomain}
              onRemove={(domain) => setDomains((current) => current.filter((value) => value !== domain))}
            />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="TLS">
                <NativeSelect
                  value={route.tls}
                  disabled={ingress !== "managed"}
                  onChange={(event) => {
                    const tls = event.target.value as T.TLSMode;
                    setRoute({
                      ...route,
                      tls,
                      certName: tls === "external" ? route.certName : undefined,
                      redirectHttps: tls === "none" ? false : route.redirectHttps,
                    });
                  }}
                >
                  <option value="none">None</option>
                  <option value="self-signed">Self-signed</option>
                  <option value="acme">ACME</option>
                  <option value="external">External certificate</option>
                </NativeSelect>
              </Field>
              {route.tls === "external" && ingress === "managed" && (
                <Field label="Certificate name" hint="Existing directory under edge/certs/external.">
                  <Input
                    value={route.certName ?? ""}
                    onChange={(event) => setRoute({ ...route, certName: event.target.value })}
                  />
                </Field>
              )}
              <Field
                label="Initial data binding"
                hint="More bindings can be added later; existing bindings cannot be removed."
              >
                <NativeSelect value={binding} onChange={(event) => setBinding(event.target.value)}>
                  <option value="sqlite">SQLite (private file)</option>
                  {(services.data?.services ?? [])
                    .filter((service) => service.engine === "mysql" || service.engine === "postgres")
                    .map((service) => (
                      <option key={service.name} value={`${service.engine}:${service.name}`}>
                        {service.engine} {service.version} ({service.name})
                      </option>
                    ))}
                  <option value="none">No database</option>
                </NativeSelect>
              </Field>
            </div>
            <div className="flex flex-wrap gap-5 text-sm">
              <label className="flex items-center gap-2">
                <Checkbox
                  checked={route.redirectHttps}
                  disabled={route.tls === "none" || ingress !== "managed"}
                  onCheckedChange={(checked) => setRoute({ ...route, redirectHttps: checked === true })}
                />
                Redirect HTTP to HTTPS
              </label>
              <label className="flex items-center gap-2">
                <Checkbox
                  checked={route.accessLog}
                  disabled={ingress !== "managed"}
                  onCheckedChange={(checked) => setRoute({ ...route, accessLog: checked === true })}
                />
                Access log
              </label>
            </div>
            <Button
              type="button"
              variant="outline"
              className="justify-self-start"
              onClick={() => setShowAdvanced((value) => !value)}
              aria-expanded={showAdvanced}
            >
              {showAdvanced ? "Hide" : "Show"} advanced resources
            </Button>
            {showAdvanced && (
              <div className="grid gap-4 rounded-lg border p-4 sm:grid-cols-3">
                <Field label="Memory (MB)">
                  <Input
                    type="number"
                    min="64"
                    max="262144"
                    value={resources.memoryMb}
                    onChange={(event) => setResources({ ...resources, memoryMb: Number(event.target.value) })}
                  />
                </Field>
                <Field label="CPU (millicores)">
                  <Input
                    type="number"
                    min="50"
                    max="256000"
                    value={resources.cpuMillis}
                    onChange={(event) => setResources({ ...resources, cpuMillis: Number(event.target.value) })}
                  />
                </Field>
                <Field label="Process limit">
                  <Input
                    type="number"
                    min="32"
                    max="65536"
                    value={resources.pids}
                    onChange={(event) => setResources({ ...resources, pids: Number(event.target.value) })}
                  />
                </Field>
              </div>
            )}
            {ingress === "managed" && domains.length === 0 && (
              <Alert variant="destructive">Add at least one domain for managed ingress.</Alert>
            )}
          </section>
        )}

        {step === 3 && (
          <section className="grid gap-5">
            <div>
              <h2 className="m-0 text-lg font-semibold">Review</h2>
              <p className="mt-1 text-sm text-muted-foreground">
                The application is created stopped and unpublished. Start it, verify readiness, then publish.
              </p>
            </div>
            <dl className="grid gap-3 rounded-lg border p-4 text-sm sm:grid-cols-2">
              <Review label="Slug" value={slug} />
              <Review label="Home" value={`/home/${slug}`} />
              <Review
                label="Runtime"
                value={
                  kind === "php-fpm"
                    ? `PHP ${php.version} · ${php.pool}`
                    : `${http.toolchain} ${http.version} · ${http.argv.join(" ")}`
                }
              />
              <Review label="Ingress" value={`${ingress}${domains.length ? ` · ${domains.join(", ")}` : ""}`} />
              <Review label="TLS" value={route.tls} />
              <Review label="Data" value={binding === "none" ? "No initial binding" : binding} />
              <Review
                label="Resources"
                value={`${resources.memoryMb} MB · ${resources.cpuMillis}m CPU · ${resources.pids} pids`}
              />
            </dl>
            <Alert>Next steps: start → verify → publish. Creation never starts or publishes the app implicitly.</Alert>
            {create.error && <Alert variant="destructive">{messageOf(create.error)}</Alert>}
          </section>
        )}

        <div className="mt-6 flex justify-between gap-3 border-t pt-4">
          <Button
            type="button"
            variant="outline"
            onClick={() => (step === 0 ? navigate("/apps") : setStep((value) => value - 1))}
          >
            <ChevronLeft />
            {step === 0 ? "Cancel" : "Back"}
          </Button>
          <Button type="submit" disabled={!currentValid || create.isPending}>
            {step === 3 ? (
              "Create application"
            ) : (
              <>
                Continue
                <ChevronRight />
              </>
            )}
          </Button>
        </div>
      </form>
    </Page>
  );
}

function ArgvEditor({ value, onChange }: { value: string[]; onChange: (value: string[]) => void }) {
  return (
    <fieldset className="grid gap-2">
      <legend className="mb-1 text-sm font-medium">Command arguments</legend>
      {value.map((argument, index) => (
        <div key={index} className="flex gap-2">
          <Input
            aria-label={`Argument ${index + 1}`}
            value={argument}
            onChange={(event) =>
              onChange(value.map((item, itemIndex) => (itemIndex === index ? event.target.value : item)))
            }
          />
          <Button
            type="button"
            size="icon-sm"
            variant="outline"
            aria-label={`Remove argument ${index + 1}`}
            disabled={value.length === 1}
            onClick={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}
          >
            <X />
          </Button>
        </div>
      ))}
      <Button
        type="button"
        size="sm"
        variant="outline"
        className="justify-self-start"
        onClick={() => onChange([...value, ""])}
      >
        <Plus />
        Add argument
      </Button>
    </fieldset>
  );
}
function DomainsInput({
  domains,
  draft,
  error,
  onDraft,
  onKeyDown,
  onAdd,
  onRemove,
}: {
  domains: string[];
  draft: string;
  error: string;
  onDraft: (value: string) => void;
  onKeyDown: (event: KeyboardEvent<HTMLInputElement>) => void;
  onAdd: () => void;
  onRemove: (domain: string) => void;
}) {
  return (
    <Field label="Domains" hint="The first domain is primary. Press Enter or comma to add.">
      <div className="rounded-md border border-input p-2">
        <div className="mb-2 flex flex-wrap gap-1">
          {domains.map((domain, index) => (
            <span key={domain} className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-1 text-xs">
              <code>{domain}</code>
              {index === 0 && <span className="text-muted-foreground">primary</span>}
              <button type="button" aria-label={`Remove ${domain}`} onClick={() => onRemove(domain)}>
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
        <div className="flex gap-2">
          <Input
            className="border-0 shadow-none focus-visible:ring-0"
            value={draft}
            placeholder="app.example.com"
            onChange={(event) => onDraft(event.target.value)}
            onKeyDown={onKeyDown}
            onBlur={() => draft.trim() && onAdd()}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!draft.trim()}
            onMouseDown={(event) => event.preventDefault()}
            onClick={onAdd}
          >
            Add
          </Button>
        </div>
      </div>
      {error && <span className="text-xs text-destructive">{error}</span>}
    </Field>
  );
}
function Review({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="m-0 mt-1 break-words font-medium">{value}</dd>
    </div>
  );
}
