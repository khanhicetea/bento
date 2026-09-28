import { useState, type FormEvent, type KeyboardEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Code2, Database, Globe, HardDrive, Lock, Plus, Server, X } from "lucide-react";
import { useLocation } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, Field, KeyValues, PageHeader } from "../../components/DomainState.tsx";
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
const stepLabels = ["Basics", "Runtime", "Routing & data", "Review"];

function slugError(slug: string): string | null {
  if (!/^[a-z][a-z0-9-]{1,30}[a-z0-9]$/.test(slug) || slug.includes("--"))
    return "3–32 lowercase letters, digits or single hyphens; start with a letter.";
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
  const domains = useDomainDraft([]);
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
      domains: domains.list,
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
    (ingress !== "managed" || domains.list.length > 0) &&
    (route.tls !== "external" || /^[a-z0-9][a-z0-9._-]{0,63}$/.test(route.certName ?? "")) &&
    resources.memoryMb >= 64 &&
    resources.memoryMb <= 262144 &&
    resources.cpuMillis >= 50 &&
    resources.cpuMillis <= 256000 &&
    resources.pids >= 32 &&
    resources.pids <= 65536;
  const currentValid = step === 0 ? identityValid : step === 1 ? detailsValid : step === 2 ? routingValid : true;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (step < 3) {
      if (currentValid) setStep((value) => value + 1);
      return;
    }
    create.mutate(undefined, { onSuccess: () => navigate("/apps") });
  }

  const toolchains = catalog.data?.toolchains ?? {};
  const managed = ingress === "managed";
  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader back={{ href: "/apps", label: "Apps" }} title="New app" />
      <ol className="steps" aria-label={`Step ${step + 1} of ${stepLabels.length}`}>
        {stepLabels.map((label, index) => (
          <li key={label} data-done={index <= step} aria-current={index === step ? "step" : undefined}>
            <span className="sr-only">{label}</span>
          </li>
        ))}
      </ol>
      <form onSubmit={submit}>
        <div className="box">
          {step === 0 && (
            <>
              <Cell title="Name">
                <Field label="Slug" hint={slug && !slugError(slug) ? `/home/${slug}` : undefined}>
                  <Input
                    autoFocus
                    placeholder="my-app"
                    value={slug}
                    aria-invalid={slug.length > 0 && !!slugError(slug)}
                    onChange={(event) => setSlug(event.target.value.toLowerCase())}
                  />
                </Field>
                {slug.length > 0 && slugError(slug) && <p className="note note--bad mt-2">{slugError(slug)}</p>}
              </Cell>
              <Cell title="Runtime">
                <div className="choices">
                  <Choice
                    name="runtime"
                    checked={kind === "php-fpm"}
                    onChange={() => setKind("php-fpm")}
                    icon={<Code2 className="size-4" />}
                    title="PHP"
                    detail="PHP-FPM + Nginx"
                  />
                  <Choice
                    name="runtime"
                    checked={kind === "http-process"}
                    onChange={() => setKind("http-process")}
                    icon={<Server className="size-4" />}
                    title="HTTP process"
                    detail="Node, Bun, Python…"
                  />
                </div>
              </Cell>
            </>
          )}

          {step === 1 &&
            (kind === "php-fpm" ? (
              <Cell title="PHP">
                <div className="grid-3">
                  <Field label="Version">
                    <NativeSelect
                      className="w-full"
                      value={php.version}
                      onChange={(event) => setPhp({ ...php, version: event.target.value })}
                    >
                      {(catalog.data?.phpVersions ?? [php.version]).map((version) => (
                        <option key={version}>{version}</option>
                      ))}
                    </NativeSelect>
                  </Field>
                  <Field label="Pool">
                    <NativeSelect
                      className="w-full"
                      value={php.pool}
                      onChange={(event) => setPhp({ ...php, pool: event.target.value })}
                    >
                      {(catalog.data?.poolProfiles ?? [php.pool]).map((pool) => (
                        <option key={pool}>{pool}</option>
                      ))}
                    </NativeSelect>
                  </Field>
                  <Field label="Routing">
                    <NativeSelect
                      className="w-full"
                      value={php.routing}
                      onChange={(event) => setPhp({ ...php, routing: event.target.value })}
                    >
                      <option value="front-controller">Front controller</option>
                      <option value="legacy">Legacy .php files</option>
                    </NativeSelect>
                  </Field>
                  <Field label="Document root">
                    <Input
                      value={php.documentRoot}
                      onChange={(event) => setPhp({ ...php, documentRoot: event.target.value })}
                    />
                  </Field>
                  <Field label="Release symlink">
                    <Input
                      placeholder="optional"
                      value={php.releaseSymlink ?? ""}
                      onChange={(event) => setPhp({ ...php, releaseSymlink: event.target.value || undefined })}
                    />
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
                </div>
              </Cell>
            ) : (
              <>
                <Cell title="Toolchain">
                  <div className="grid-2">
                    <Field label="Toolchain">
                      <NativeSelect
                        className="w-full"
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
                        className="w-full"
                        value={http.version}
                        onChange={(event) => setHttp({ ...http, version: event.target.value })}
                      >
                        {(toolchains[http.toolchain] ?? [http.version]).map((version) => (
                          <option key={version}>{version}</option>
                        ))}
                      </NativeSelect>
                    </Field>
                  </div>
                </Cell>
                <Cell title="Command">
                  <ArgvEditor value={http.argv} onChange={(argv) => setHttp({ ...http, argv })} />
                </Cell>
                <Cell title="Serving">
                  <div className="grid-3">
                    <Field label="Port">
                      <Input
                        type="number"
                        min="1024"
                        max="65535"
                        value={http.port}
                        onChange={(event) => setHttp({ ...http, port: Number(event.target.value) })}
                      />
                    </Field>
                    <Field label="Working dir">
                      <Input
                        value={http.workdir}
                        placeholder="."
                        onChange={(event) => setHttp({ ...http, workdir: event.target.value })}
                      />
                    </Field>
                    <Field label="Ready path">
                      <Input
                        placeholder="/"
                        value={http.readyPath ?? ""}
                        onChange={(event) => setHttp({ ...http, readyPath: event.target.value || undefined })}
                      />
                    </Field>
                  </div>
                </Cell>
              </>
            ))}

          {step === 2 && (
            <>
              <Cell title="Access">
                <div className="grid gap-4">
                  <div className="choices">
                    <Choice
                      name="ingress"
                      checked={managed}
                      onChange={() => setIngress("managed")}
                      icon={<Globe className="size-4" />}
                      title="Public"
                      detail="Bento edge + TLS"
                    />
                    <Choice
                      name="ingress"
                      checked={ingress === "external"}
                      onChange={() => setIngress("external")}
                      icon={<Server className="size-4" />}
                      title="External"
                      detail="Tunnel or own proxy"
                    />
                    <Choice
                      name="ingress"
                      checked={ingress === "none"}
                      onChange={() => setIngress("none")}
                      icon={<Lock className="size-4" />}
                      title="Private"
                      detail="No public route"
                    />
                  </div>
                  <DomainsInput state={domains} />
                  {managed && (
                    <>
                      <div className="grid-2">
                        <Field label="TLS">
                          <NativeSelect
                            className="w-full"
                            value={route.tls}
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
                            <option value="external">External cert</option>
                          </NativeSelect>
                        </Field>
                        {route.tls === "external" && (
                          <Field label="Certificate name">
                            <Input
                              value={route.certName ?? ""}
                              onChange={(event) => setRoute({ ...route, certName: event.target.value })}
                            />
                          </Field>
                        )}
                      </div>
                      <div className="flex flex-wrap gap-5">
                        <label className="check">
                          <Checkbox
                            checked={route.redirectHttps}
                            disabled={route.tls === "none"}
                            onCheckedChange={(checked) => setRoute({ ...route, redirectHttps: checked === true })}
                          />
                          HTTPS redirect
                        </label>
                        <label className="check">
                          <Checkbox
                            checked={route.accessLog}
                            onCheckedChange={(checked) => setRoute({ ...route, accessLog: checked === true })}
                          />
                          Access log
                        </label>
                      </div>
                      {domains.list.length === 0 && <p className="note note--bad">Add at least one domain.</p>}
                    </>
                  )}
                </div>
              </Cell>
              <Cell title="Data">
                <div className="choices">
                  <Choice
                    name="binding"
                    checked={binding === "sqlite"}
                    onChange={() => setBinding("sqlite")}
                    icon={<HardDrive className="size-4" />}
                    title="SQLite"
                    detail="Private file"
                  />
                  {(services.data?.services ?? [])
                    .filter((service) => service.engine === "mysql" || service.engine === "postgres")
                    .map((service) => {
                      const value = `${service.engine}:${service.name}`;
                      return (
                        <Choice
                          key={value}
                          name="binding"
                          checked={binding === value}
                          onChange={() => setBinding(value)}
                          icon={<Database className="size-4" />}
                          title={`${service.engine === "postgres" ? "Postgres" : "MySQL"} ${service.version}`}
                          detail={service.name}
                        />
                      );
                    })}
                  <Choice
                    name="binding"
                    checked={binding === "none"}
                    onChange={() => setBinding("none")}
                    icon={<X className="size-4" />}
                    title="None"
                    detail="Add later"
                  />
                </div>
              </Cell>
              <Cell
                title="Limits"
                action={
                  <button
                    type="button"
                    className="note underline"
                    aria-expanded={showAdvanced}
                    onClick={() => setShowAdvanced((value) => !value)}
                  >
                    {showAdvanced ? "Hide" : "Edit"}
                  </button>
                }
              >
                {showAdvanced ? (
                  <div className="grid-3">
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
                    <Field label="Processes">
                      <Input
                        type="number"
                        min="32"
                        max="65536"
                        value={resources.pids}
                        onChange={(event) => setResources({ ...resources, pids: Number(event.target.value) })}
                      />
                    </Field>
                  </div>
                ) : (
                  <p className="note">
                    {resources.memoryMb} MB · {resources.cpuMillis / 1000} CPU · {resources.pids} processes
                  </p>
                )}
              </Cell>
            </>
          )}

          {step === 3 && (
            <Cell title="Review">
              <KeyValues
                items={[
                  ["Slug", <strong>{slug}</strong>],
                  ["Home", <code>/home/{slug}</code>],
                  [
                    "Runtime",
                    kind === "php-fpm"
                      ? `PHP ${php.version} · ${php.pool}`
                      : `${http.toolchain} ${http.version} · ${http.argv.join(" ")}`,
                  ],
                  ["Access", `${ingress}${domains.list.length ? ` · ${domains.list.join(", ")}` : ""}`],
                  ["TLS", route.tls],
                  ["Data", binding === "none" ? "None" : binding],
                  ["Limits", `${resources.memoryMb} MB · ${resources.cpuMillis}m CPU · ${resources.pids} pids`],
                ]}
              />
              <p className="note mt-4">Created stopped and private. Start → check → publish.</p>
              {create.error && (
                <Alert variant="destructive" className="mt-3">
                  {messageOf(create.error)}
                </Alert>
              )}
            </Cell>
          )}

          <div className="cell cell--muted actions actions--between py-3!">
            <Button
              type="button"
              variant="ghost"
              onClick={() => (step === 0 ? navigate("/apps") : setStep((value) => value - 1))}
            >
              <ChevronLeft />
              {step === 0 ? "Cancel" : "Back"}
            </Button>
            <Button type="submit" disabled={!currentValid || create.isPending}>
              {step === 3 ? (
                "Create app"
              ) : (
                <>
                  Next
                  <ChevronRight />
                </>
              )}
            </Button>
          </div>
        </div>
      </form>
    </div>
  );
}

function Choice({
  name,
  checked,
  onChange,
  icon,
  title,
  detail,
}: {
  name: string;
  checked: boolean;
  onChange: () => void;
  icon: React.ReactNode;
  title: string;
  detail: string;
}) {
  return (
    <label className="choice">
      <input className="sr-only" type="radio" name={name} checked={checked} onChange={onChange} />
      <strong>
        {icon}
        {title}
      </strong>
      <small>{detail}</small>
    </label>
  );
}

export function ArgvEditor({ value, onChange }: { value: string[]; onChange: (value: string[]) => void }) {
  return (
    <fieldset className="grid gap-2">
      <legend className="mb-2 text-sm font-semibold">
        Command <span className="note font-normal">· run directly, no shell</span>
      </legend>
      {value.map((argument, index) => (
        <div key={index} className="flex gap-2">
          <Input
            className="font-mono"
            aria-label={`Argument ${index + 1}`}
            value={argument}
            onChange={(event) =>
              onChange(value.map((item, itemIndex) => (itemIndex === index ? event.target.value : item)))
            }
          />
          <Button
            type="button"
            size="icon"
            variant="ghost"
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
        variant="ghost"
        className="justify-self-start"
        onClick={() => onChange([...value, ""])}
      >
        <Plus />
        Argument
      </Button>
    </fieldset>
  );
}

/** Local draft state for a domain chip input. */
export function useDomainDraft(initial: string[]) {
  const [list, setList] = useState(initial);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  function add() {
    const value = draft.trim().toLowerCase().replace(/\.$/, "");
    if (!value) return;
    if (!validDomain(value)) {
      setError("Not a valid hostname.");
      return;
    }
    if (!list.includes(value)) setList((current) => [...current, value]);
    setDraft("");
    setError("");
  }
  return {
    list,
    draft,
    error,
    add,
    setDraft,
    remove: (domain: string) => setList((current) => current.filter((value) => value !== domain)),
  };
}

export function DomainsInput({ state }: { state: ReturnType<typeof useDomainDraft> }) {
  function keyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Enter" || event.key === ",") {
      event.preventDefault();
      state.add();
    }
  }
  return (
    <Field
      label="Domains"
      hint={state.error ? <span className="text-destructive">{state.error}</span> : "First is primary"}
    >
      <div className="grid gap-2">
        {state.list.length > 0 && (
          <div className="chips">
            {state.list.map((domain, index) => (
              <span key={domain} className="chip">
                {domain}
                {index === 0 && <b className="text-primary">•</b>}
                <button type="button" aria-label={`Remove ${domain}`} onClick={() => state.remove(domain)}>
                  <X className="size-3" />
                </button>
              </span>
            ))}
          </div>
        )}
        <div className="flex gap-2">
          <Input
            value={state.draft}
            placeholder="app.example.com"
            onChange={(event) => state.setDraft(event.target.value)}
            onKeyDown={keyDown}
            onBlur={() => state.draft.trim() && state.add()}
          />
          <Button
            type="button"
            variant="outline"
            disabled={!state.draft.trim()}
            onMouseDown={(event) => event.preventDefault()}
            onClick={state.add}
          >
            Add
          </Button>
        </div>
      </div>
    </Field>
  );
}
