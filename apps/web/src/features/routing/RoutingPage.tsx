import { useState, type ReactNode } from "react";
import type { RoutingOverview, RoutingProxy } from "@bento/shared";
import {
  ArrowUpRight,
  ChevronDown,
  Globe2,
  LockKeyhole,
  Network,
  Pencil,
  Power,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  Trash2,
  Waypoints,
  X,
} from "lucide-react";
import { DomainError, DomainLoading, EmptyPanel, StackNotReady } from "../../components/DomainState.tsx";
import { ProxyEditor } from "./ProxyEditor.tsx";
import { RemoveProxyDialog } from "./RemoveProxyDialog.tsx";
import { useRouting } from "./useRouting.ts";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

type RoutingView = "all" | "domains" | "proxies";
type Domain = RoutingOverview["domains"][number];
type Proxy = RoutingOverview["proxies"][number];
type TlsMode = Domain["tls"];

const tlsModes: TlsMode[] = ["acme", "external", "self-ca", "shared"];

export function RoutingPage() {
  const { query, error, saving, removing, changing, saveProxy, setProxyEnabled, removeProxy, resetErrors } =
    useRouting();
  const [search, setSearch] = useState("");
  const [view, setView] = useState<RoutingView>("all");
  const [editorTarget, setEditorTarget] = useState<RoutingProxy | "create" | null>(null);
  const [removeTarget, setRemoveTarget] = useState<RoutingProxy | null>(null);
  const data = query.data;

  function startCreating() {
    resetErrors();
    setEditorTarget("create");
  }

  if (!data && query.isPending) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="routing and TLS" />
      </section>
    );
  }
  if (!data && query.error) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  }
  if (!data) return null;

  const normalizedSearch = search.trim().toLowerCase();
  const showDomains = view === "all" || view === "domains";
  const showProxies = view === "all" || view === "proxies";
  const domains = data.domains.filter((domain) =>
    matches(
      normalizedSearch,
      domain.domain,
      domain.owner,
      domain.ownerKind,
      domain.tls,
      domain.primary ? "primary" : "alias",
    ),
  );
  const proxies = data.proxies.filter((proxy) =>
    matches(normalizedSearch, proxy.name, proxy.domain, proxy.aliases.join(" "), proxy.upstreams.join(" "), proxy.tls),
  );
  const visibleItems = (showDomains ? domains.length : 0) + (showProxies ? proxies.length : 0);
  const filterTotal = (showDomains ? data.domains.length : 0) + (showProxies ? data.proxies.length : 0);
  const filters = [
    { value: "all", label: "All", count: data.domains.length + data.proxies.length },
    { value: "domains", label: "Domains", count: data.domains.length },
    { value: "proxies", label: "Reverse proxies", count: data.proxies.length },
  ] as const;
  const noMatches = normalizedSearch.length > 0 && visibleItems === 0;

  return (
    <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4" aria-live="polite">
      <div className="flex items-end justify-between gap-6 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Control plane / Networking
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">Routing &amp; TLS</h2>
          <p className="m-0 mt-2 max-w-[680px] text-sm text-muted-foreground">
            Understand how traffic enters the stack, which domains it serves, and how TLS is configured.
          </p>
        </div>
        <div className="flex gap-2 max-[760px]:w-full">
          <Button
            className="max-[760px]:flex-1"
            variant="outline"
            disabled={query.isFetching}
            onClick={() => void query.refetch()}
          >
            {query.isFetching ? <RefreshCw className="animate-spin" /> : <RefreshCw />}
            Refresh inventory
          </Button>
          <Button className="max-[760px]:flex-1" disabled={!data.initialized} onClick={startCreating}>
            <span aria-hidden="true">+</span> Add proxy
          </Button>
        </div>
      </div>

      {error && editorTarget === null && removeTarget === null && (
        <Alert className="mt-6" variant="destructive">
          <span>{error}</span>
        </Alert>
      )}

      {!data.initialized ? (
        <div className="mt-8">
          <StackNotReady stackRoot={data.stackRoot} error={data.error} />
        </div>
      ) : (
        <>
          <div className="mt-8 grid grid-cols-4 gap-3 max-[1050px]:grid-cols-2 max-[560px]:grid-cols-1">
            <Summary value={data.domains.length} label="Claimed domains" icon={<Globe2 className="size-4" />} />
            <Summary
              value={data.domains.filter((domain) => domain.tls === "acme").length}
              label="ACME domains"
              icon={<ShieldCheck className="size-4" />}
            />
            <Summary value={data.proxies.length} label="Reverse proxies" icon={<Network className="size-4" />} />
            <Summary
              value={data.ingress ? (data.ingress.http3 ? "Enabled" : "Disabled") : "Unknown"}
              label="HTTP/3"
              icon={<Waypoints className="size-4" />}
              tone={data.ingress?.http3 ? "success" : "default"}
            />
          </div>

          <div className="mt-10 flex flex-wrap items-center justify-between gap-3">
            <div
              className="flex items-center gap-1 rounded-lg border border-border bg-muted/50 p-1"
              role="group"
              aria-label="Filter routing inventory"
            >
              {filters.map((filter) => (
                <button
                  key={filter.value}
                  type="button"
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${view === filter.value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                  aria-pressed={view === filter.value}
                  onClick={() => setView(filter.value)}
                >
                  {filter.label}
                  <span className="ml-1.5 opacity-60">{filter.count}</span>
                </button>
              ))}
            </div>
            <div className="flex h-[2.875rem] min-w-[min(100%,360px)] flex-1 items-center gap-2 rounded-xl border border-border bg-card px-3 py-1.5 shadow-sm focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20 max-[760px]:min-w-full">
              <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              <Input
                className="h-8 min-w-0 flex-1 border-0 bg-transparent px-1 shadow-none focus-visible:ring-0"
                type="search"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search domains, owners, or upstreams…"
                aria-label="Search routing inventory"
              />
              {search && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  className="shrink-0 rounded-full"
                  aria-label="Clear routing search"
                  onClick={() => setSearch("")}
                >
                  <X />
                </Button>
              )}
            </div>
          </div>
          <div className="mb-4 mt-3 flex items-center justify-between gap-4 text-xs text-muted-foreground">
            <p className="m-0">
              Showing <strong className="font-semibold text-foreground">{visibleItems}</strong> of {filterTotal} routing
              entries
            </p>
            {view !== "all" && (
              <button
                type="button"
                className="underline underline-offset-2 hover:text-foreground"
                onClick={() => setView("all")}
              >
                Show all
              </button>
            )}
          </div>

          {noMatches ? (
            <NoMatches onClear={() => setSearch("")} />
          ) : (
            <div>
              {showDomains && (
                <InventorySection
                  id="domains"
                  eyebrow="Public entry points"
                  title="Domains"
                  description="Every hostname published by an application or reverse proxy."
                  count={domains.length}
                  itemLabel="domain"
                  icon={<Globe2 className="size-4" />}
                >
                  {domains.length ? (
                    <DomainsTable domains={domains} />
                  ) : (
                    <CollectionEmpty>
                      {normalizedSearch ? "No domains match your search." : "No domains configured."}
                    </CollectionEmpty>
                  )}
                </InventorySection>
              )}

              {showProxies && (
                <InventorySection
                  id="reverse-proxies"
                  eyebrow="Traffic forwarding"
                  title="Reverse proxies"
                  description="Custom upstreams that receive traffic through the shared ingress."
                  count={proxies.length}
                  itemLabel="proxy"
                  icon={<Network className="size-4" />}
                >
                  {proxies.length ? (
                    <div className="grid grid-cols-2 items-stretch gap-5 max-[900px]:grid-cols-1">
                      {proxies.map((proxy) => (
                        <ProxyCard
                          key={proxy.name}
                          proxy={proxy}
                          busy={changing === proxy.name}
                          onEdit={() => {
                            resetErrors();
                            setEditorTarget(proxy);
                          }}
                          onToggle={() => setProxyEnabled(proxy)}
                          onRemove={() => {
                            resetErrors();
                            setRemoveTarget(proxy);
                          }}
                        />
                      ))}
                    </div>
                  ) : (
                    <CollectionEmpty>
                      {normalizedSearch ? "No reverse proxies match your search." : "No reverse proxies configured."}
                    </CollectionEmpty>
                  )}
                </InventorySection>
              )}

              <details className="group mt-10 rounded-2xl border border-border bg-card shadow-sm">
                <summary className="flex cursor-pointer list-none items-center justify-between gap-4 rounded-2xl px-5 py-4 outline-none transition-colors hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-primary">
                      <Network className="size-4" aria-hidden="true" />
                    </span>
                    <div className="min-w-0">
                      <h3 className="m-0 text-base font-semibold tracking-tight">Ingress &amp; TLS configuration</h3>
                      <p className="m-0 mt-1 truncate text-sm text-muted-foreground">
                        Network entry points and certificate strategies
                      </p>
                    </div>
                  </div>
                  <ChevronDown
                    className="size-5 shrink-0 text-muted-foreground transition-transform group-open:rotate-180"
                    aria-hidden="true"
                  />
                </summary>
                <div className="border-t border-border p-5 max-[760px]:p-4">
                  <div className="grid grid-cols-2 gap-5 max-[900px]:grid-cols-1">
                    <IngressCard ingress={data.ingress} />
                    <TlsCard domains={data.domains} />
                  </div>
                </div>
              </details>
            </div>
          )}
        </>
      )}
      {data.initialized && editorTarget !== null && (
        <ProxyEditor
          key={editorTarget === "create" ? "create" : editorTarget.name}
          proxy={editorTarget === "create" ? null : editorTarget}
          error={error}
          saving={saving}
          onClose={() => setEditorTarget(null)}
          onSave={saveProxy}
        />
      )}
      {removeTarget && (
        <RemoveProxyDialog
          key={removeTarget.name}
          proxy={removeTarget}
          error={error}
          removing={removing}
          onClose={() => setRemoveTarget(null)}
          onRemove={removeProxy}
        />
      )}
    </section>
  );
}

function Summary({
  value,
  label,
  icon,
  tone = "default",
}: {
  value: number | string;
  label: string;
  icon: ReactNode;
  tone?: "default" | "success";
}) {
  return (
    <div className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3 text-card-foreground shadow-sm">
      <span
        className={`grid size-9 shrink-0 place-items-center rounded-lg ${tone === "success" ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" : "bg-muted text-muted-foreground"}`}
      >
        {icon}
      </span>
      <span className="min-w-0">
        <strong className="block text-xl leading-none tracking-tight">{value}</strong>
        <span className="mt-1 block truncate text-xs text-muted-foreground">{label}</span>
      </span>
    </div>
  );
}

function IngressCard({ ingress }: { ingress: RoutingOverview["ingress"] }) {
  const mode = ingress?.mode;
  return (
    <article className="rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <div className="flex items-start justify-between gap-4">
        <div className="flex min-w-0 items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
            <Server className="size-5" aria-hidden="true" />
          </span>
          <div className="min-w-0">
            <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              Edge network
            </p>
            <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">Ingress</h3>
          </div>
        </div>
        <Badge variant="secondary" className="shrink-0">
          {mode === "host" ? "Host network" : mode === "bridge" ? "Bridge network" : "Unknown network"}
        </Badge>
      </div>
      <p className="m-0 mt-4 text-sm text-muted-foreground">
        Shared entry point for application sites and reverse proxies.
      </p>
      <div className="mt-4 grid grid-cols-3 divide-x divide-border overflow-hidden rounded-xl border border-border bg-muted/30 max-[520px]:grid-cols-1 max-[520px]:divide-x-0 max-[520px]:divide-y">
        <Fact label="HTTP" value={port(mode, ingress?.httpPort, 80)} />
        <Fact label="HTTPS" value={port(mode, ingress?.httpsPort, 443)} />
        <Fact label="HTTP/3" value={ingress ? (ingress.http3 ? "Enabled" : "Disabled") : "Unknown"} />
      </div>
    </article>
  );
}

function TlsCard({ domains }: { domains: RoutingOverview["domains"] }) {
  return (
    <article className="rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm">
      <div className="flex items-start gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
          <LockKeyhole className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0">
          <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            Certificate strategy
          </p>
          <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">TLS configuration</h3>
        </div>
      </div>
      <p className="m-0 mt-4 text-sm text-muted-foreground">
        Configured certificate strategy for each published domain. Certificate health is not inferred from
        configuration.
      </p>
      <div className="mt-4 grid grid-cols-2 gap-2">
        {tlsModes.map((mode) => {
          const count = domains.filter((domain) => domain.tls === mode).length;
          return (
            <div
              key={mode}
              className="flex items-center justify-between gap-3 rounded-xl border border-border bg-muted/30 px-3 py-2.5"
            >
              <TlsBadge mode={mode} />
              <strong className="text-sm">{count}</strong>
            </div>
          );
        })}
      </div>
    </article>
  );
}

function InventorySection({
  id,
  eyebrow,
  title,
  description,
  count,
  itemLabel,
  icon,
  children,
}: {
  id: string;
  eyebrow: string;
  title: string;
  description: string;
  count: number;
  itemLabel: string;
  icon: ReactNode;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id}>
      <div className="mt-10 flex items-end justify-between gap-4 max-[760px]:items-start">
        <div className="flex min-w-0 items-start gap-3">
          <span className="mt-1 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
            {icon}
          </span>
          <div className="min-w-0">
            <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              {eyebrow}
            </p>
            <h3 id={id} className="m-0 mt-1 text-lg font-semibold tracking-tight">
              {title}
            </h3>
            <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
          </div>
        </div>
        <Badge variant="secondary" className="shrink-0">
          {count} {count === 1 ? itemLabel : `${itemLabel}s`}
        </Badge>
      </div>
      <div className="mt-4">{children}</div>
    </section>
  );
}

function DomainsTable({ domains }: { domains: Domain[] }) {
  return (
    <article className="overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-sm">
      <div className="overflow-auto">
        <Table className="min-w-[760px] bg-card">
          <TableHeader>
            <TableRow>
              <TableHead>Domain</TableHead>
              <TableHead>Owner</TableHead>
              <TableHead>TLS mode</TableHead>
              <TableHead>Role</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {domains.map((domain) => (
              <TableRow key={domain.domain}>
                <TableCell>
                  <a
                    className="group flex w-fit max-w-[24rem] items-center gap-1.5 no-underline hover:text-primary hover:underline hover:underline-offset-2"
                    href={`https://${domain.domain}`}
                    target="_blank"
                    rel="noreferrer"
                    title={`Open ${domain.domain}`}
                  >
                    <Globe2 className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    <strong className="truncate">{domain.domain}</strong>
                    <ArrowUpRight className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    <span className="sr-only">(opens in a new tab)</span>
                  </a>
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                      {domain.ownerKind === "application" ? (
                        <Server className="size-3.5" aria-hidden="true" />
                      ) : (
                        <Network className="size-3.5" aria-hidden="true" />
                      )}
                    </span>
                    <div className="min-w-0">
                      <strong className="block max-w-[14rem] truncate text-sm" title={domain.owner}>
                        {domain.owner}
                      </strong>
                      <span className="block text-xs capitalize text-muted-foreground">{domain.ownerKind}</span>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  <TlsBadge mode={domain.tls} />
                </TableCell>
                <TableCell>
                  <Badge variant={domain.primary ? "secondary" : "outline"}>
                    {domain.primary ? "Primary" : "Alias"}
                  </Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </article>
  );
}

function ProxyCard({
  proxy,
  busy,
  onEdit,
  onToggle,
  onRemove,
}: {
  proxy: Proxy;
  busy: boolean;
  onEdit: () => void;
  onToggle: () => void;
  onRemove: () => void;
}) {
  return (
    <article
      className={`flex min-w-0 flex-col rounded-2xl border bg-card p-5 text-card-foreground shadow-sm ${proxy.enabled ? "border-emerald-500/25" : "border-border opacity-80"}`}
      aria-busy={busy}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-muted text-primary">
            <Network className="size-4" aria-hidden="true" />
          </span>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <strong className="truncate text-sm font-semibold" title={proxy.name}>
                {proxy.name}
              </strong>
              <Badge variant={proxy.enabled ? "secondary" : "outline"}>{proxy.enabled ? "Enabled" : "Disabled"}</Badge>
            </div>
            <a
              className="mt-1 flex min-w-0 items-center gap-1 text-xs text-muted-foreground no-underline hover:text-primary hover:underline hover:underline-offset-2"
              href={`https://${proxy.domain}`}
              target="_blank"
              rel="noreferrer"
              title={`Open ${proxy.domain}`}
            >
              <Globe2 className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="truncate">{proxy.domain}</span>
              <ArrowUpRight className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="sr-only">(opens in a new tab)</span>
            </a>
          </div>
        </div>
        <TlsBadge mode={proxy.tls} />
      </div>

      <div className="mt-5">
        <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.12em] text-muted-foreground">Upstreams</p>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {proxy.upstreams.map((upstream) => (
            <code
              className="max-w-full break-all rounded-lg border border-border bg-muted/40 px-2.5 py-1 text-xs"
              key={upstream}
            >
              {upstream}
            </code>
          ))}
        </div>
      </div>

      {proxy.aliases.length > 0 && (
        <div className="mt-5">
          <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.12em] text-muted-foreground">Aliases</p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {proxy.aliases.map((alias) => (
              <Badge variant="outline" className="max-w-full" key={alias}>
                <span className="truncate">{alias}</span>
              </Badge>
            ))}
          </div>
        </div>
      )}

      <div className="mt-auto flex flex-wrap items-center gap-2 border-t border-border pt-4">
        <Badge variant={proxy.accessLog ? "secondary" : "outline"}>
          {proxy.accessLog ? "Access logs enabled" : "Access logs disabled"}
        </Badge>
        <div className="ml-auto flex gap-2">
          <Button size="sm" variant="outline" disabled={busy} onClick={onEdit}>
            <Pencil className="size-3.5" /> Edit
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={onToggle}>
            {busy ? <Spinner /> : <Power className="size-3.5" />} {proxy.enabled ? "Disable" : "Enable"}
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            className="text-destructive hover:text-destructive"
            disabled={busy}
            aria-label={`Delete ${proxy.name}`}
            onClick={onRemove}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>
    </article>
  );
}

function TlsBadge({ mode }: { mode: TlsMode }) {
  const classes: Record<TlsMode, string> = {
    acme: "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
    external: "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300",
    "self-ca": "border-violet-500/25 bg-violet-500/10 text-violet-700 dark:text-violet-300",
    shared: "border-border bg-muted text-muted-foreground",
  };
  return (
    <Badge variant="outline" className={`gap-1.5 ${classes[mode]}`}>
      <LockKeyhole className="size-3" aria-hidden="true" />
      {tlsLabel(mode)}
    </Badge>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 p-3">
      <span className="block text-[0.68rem] text-muted-foreground">{label}</span>
      <strong className="mt-1 block truncate text-sm font-medium" title={value}>
        {value}
      </strong>
    </div>
  );
}

function CollectionEmpty({ children }: { children: string }) {
  return (
    <div className="rounded-2xl border border-dashed border-border bg-card shadow-sm">
      <EmptyPanel>{children}</EmptyPanel>
    </div>
  );
}

function NoMatches({ onClear }: { onClear: () => void }) {
  return (
    <div className="rounded-2xl border border-dashed border-border bg-card px-6 py-16 text-center shadow-sm">
      <div className="mx-auto grid size-12 place-items-center rounded-2xl bg-muted text-muted-foreground">
        <Search className="size-5" aria-hidden="true" />
      </div>
      <h3 className="mb-2 mt-4 text-lg font-semibold">No matching routes</h3>
      <p className="mx-auto mb-5 max-w-md text-sm text-muted-foreground">
        Try a different search term or clear the search to see all configured domains and proxies.
      </p>
      <Button variant="outline" onClick={onClear}>
        Clear search
      </Button>
    </div>
  );
}

function tlsLabel(mode: TlsMode) {
  const labels: Record<TlsMode, string> = {
    acme: "ACME",
    external: "External",
    "self-ca": "Private CA",
    shared: "Shared starter",
  };
  return labels[mode];
}

function port(mode: "host" | "bridge" | undefined, configured: number | undefined, hostDefault: number) {
  return mode === "host" ? `Host :${hostDefault}` : configured ? `Published :${configured}` : "Not published";
}

function matches(search: string, ...values: string[]) {
  return !search || values.some((value) => value.toLowerCase().includes(search));
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
