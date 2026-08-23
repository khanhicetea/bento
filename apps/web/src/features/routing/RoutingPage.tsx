import { useQuery } from "@tanstack/react-query";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export function RoutingPage() {
  const query = useQuery(orpc.routing.overview.queryOptions({ input: {} }));
  const data = query.data;
  if (!data && query.isPending)
    return (
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="routing" />
      </section>
    );
  if (!data && query.error)
    return (
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  if (!data) return null;
  return (
    <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
      <div className="mb-4 flex items-end justify-between gap-4 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <h2>Routing and TLS</h2>
          <p className="m-0 my-1 opacity-60">
            Ingress publications, domain ownership, certificates, and reverse proxies.
          </p>
        </div>
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          Refresh
        </Button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <Metric label="Ingress mode" value={data.ingress?.mode ?? "unknown"} />
            <Metric label="Domains" value={String(data.domains.length)} />
            <Metric
              label="ACME domains"
              value={String(data.domains.filter((item) => item.tls === "acme").length)}
            />
            <Metric label="Reverse proxies" value={String(data.proxies.length)} />
          </div>
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <article className="col-span-2 rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Ingress</h2>
              <div className="grid">
                <Detail label="Network" value={data.ingress?.mode ?? "Unknown"} />
                <Detail label="HTTP" value={port(data.ingress?.mode, data.ingress?.httpPort, 80)} />
                <Detail
                  label="HTTPS"
                  value={port(data.ingress?.mode, data.ingress?.httpsPort, 443)}
                />
                <Detail label="HTTP/3" value={data.ingress?.http3 ? "Enabled" : "Disabled"} />
              </div>
            </article>
            <article className="col-span-2 rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>TLS modes</h2>
              <div className="flex flex-wrap gap-1.5">
                {(["acme", "external", "self-ca", "shared"] as const).map((mode) => (
                  <Badge variant="outline" key={mode}>
                    {mode} · {data.domains.filter((item) => item.tls === mode).length}
                  </Badge>
                ))}
              </div>
            </article>
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Domains</h2>
              {data.domains.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Domain</TableHead>
                        <TableHead>Owner</TableHead>
                        <TableHead>Type</TableHead>
                        <TableHead>TLS</TableHead>
                        <TableHead>Role</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.domains.map((domain) => (
                        <TableRow key={domain.domain}>
                          <TableCell>
                            <strong>{domain.domain}</strong>
                          </TableCell>
                          <TableCell>{domain.owner}</TableCell>
                          <TableCell>{domain.ownerKind}</TableCell>
                          <TableCell>
                            <Badge variant="outline">{domain.tls}</Badge>
                          </TableCell>
                          <TableCell>{domain.primary ? "Primary" : "Alias"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <EmptyPanel>No domains configured.</EmptyPanel>
              )}
            </article>
            <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2 max-[760px]:col-span-1">
              <h2>Reverse proxies</h2>
              {data.proxies.length ? (
                <div className="overflow-auto rounded-[0.8rem] border border-border">
                  <Table className="min-w-[650px] bg-card">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead>Domain</TableHead>
                        <TableHead>Upstreams</TableHead>
                        <TableHead>TLS</TableHead>
                        <TableHead>Access log</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.proxies.map((proxy) => (
                        <TableRow key={proxy.name}>
                          <TableCell>
                            <strong>{proxy.name}</strong>
                          </TableCell>
                          <TableCell>{proxy.domain}</TableCell>
                          <TableCell>{proxy.upstreams.join(", ")}</TableCell>
                          <TableCell>{proxy.tls}</TableCell>
                          <TableCell>{proxy.accessLog ? "Enabled" : "Disabled"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ) : (
                <EmptyPanel>No reverse proxies configured.</EmptyPanel>
              )}
            </article>
          </div>
        </>
      )}
    </section>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-border bg-card p-5 text-card-foreground">
      <div className="text-xs uppercase tracking-[0.06em] opacity-60">{label}</div>
      <div className="mt-1 text-[1.35rem] capitalize font-bold">{value}</div>
    </div>
  );
}
function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border py-2.5 text-[0.84rem] last:border-b-0">
      <span className="opacity-70">{label}</span>
      <strong className="[overflow-wrap:anywhere] text-right">{value}</strong>
    </div>
  );
}
function port(
  mode: "host" | "bridge" | undefined,
  configured: number | undefined,
  hostDefault: number,
) {
  return mode === "host"
    ? `Host :${hostDefault}`
    : configured
      ? `Published :${configured}`
      : "Internal only";
}
function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
