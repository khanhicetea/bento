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
      <section className="content">
        <DomainLoading label="routing" />
      </section>
    );
  if (!data && query.error)
    return (
      <section className="content">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  if (!data) return null;
  return (
    <section className="content">
      <div className="section-head">
        <div>
          <h2>Routing and TLS</h2>
          <p>Ingress publications, domain ownership, certificates, and reverse proxies.</p>
        </div>
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          Refresh
        </Button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="stats-grid">
            <Metric label="Ingress mode" value={data.ingress?.mode ?? "unknown"} />
            <Metric label="Domains" value={String(data.domains.length)} />
            <Metric
              label="ACME domains"
              value={String(data.domains.filter((item) => item.tls === "acme").length)}
            />
            <Metric label="Reverse proxies" value={String(data.proxies.length)} />
          </div>
          <div className="card-grid">
            <article className="panel">
              <h2>Ingress</h2>
              <div className="detail-list">
                <Detail label="Network" value={data.ingress?.mode ?? "Unknown"} />
                <Detail label="HTTP" value={port(data.ingress?.mode, data.ingress?.httpPort, 80)} />
                <Detail
                  label="HTTPS"
                  value={port(data.ingress?.mode, data.ingress?.httpsPort, 443)}
                />
                <Detail label="HTTP/3" value={data.ingress?.http3 ? "Enabled" : "Disabled"} />
              </div>
            </article>
            <article className="panel">
              <h2>TLS modes</h2>
              <div className="pill-row">
                {(["acme", "external", "self-ca", "shared"] as const).map((mode) => (
                  <Badge variant="outline" key={mode}>
                    {mode} · {data.domains.filter((item) => item.tls === mode).length}
                  </Badge>
                ))}
              </div>
            </article>
            <article className="panel full">
              <h2>Domains</h2>
              {data.domains.length ? (
                <div className="table-wrap">
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
            <article className="panel full">
              <h2>Reverse proxies</h2>
              {data.proxies.length ? (
                <div className="table-wrap">
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
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value metric-text">{value}</div>
    </div>
  );
}
function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="detail-row">
      <span>{label}</span>
      <strong>{value}</strong>
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
