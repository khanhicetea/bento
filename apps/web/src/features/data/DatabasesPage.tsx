import { useQuery } from "@tanstack/react-query";
import { Link } from "wouter";
import {
  DomainError,
  DomainLoading,
  EmptyPanel,
  StackNotReady,
} from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { DatabaseManager } from "./DatabaseManager.tsx";
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
import { Spinner } from "@/components/ui/spinner";

export function DatabasesPage() {
  const query = useQuery(orpc.data.overview.queryOptions({ input: {} }));
  const data = query.data;

  if (!data && query.isPending) {
    return (
      <section className="content">
        <DomainLoading label="databases" />
      </section>
    );
  }
  if (!data && query.error) {
    return (
      <section className="content">
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      </section>
    );
  }
  if (!data) return null;

  const relationalBindings = data.bindings.filter(
    (binding) => binding.engine === "mysql" || binding.engine === "postgres",
  );
  const fileBindings = data.bindings.filter(
    (binding) => binding.engine === "sqlite" || binding.engine === "litestream",
  );
  const databaseCount = data.bindings.reduce(
    (total, binding) => total + binding.resources.length,
    0,
  );

  return (
    <section className="content">
      <div className="section-head">
        <div>
          <h2>Databases</h2>
          <p>
            See database usage, create backups, inspect runtimes, and manage recovery in one place.
          </p>
        </div>
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          {query.isFetching && <Spinner />}
          Refresh inventory
        </Button>
      </div>
      {!data.initialized ? (
        <StackNotReady stackRoot={data.stackRoot} error={data.error} />
      ) : (
        <>
          <div className="stats-grid">
            <Metric label="Databases" value={databaseCount} />
            <Metric label="Managed services" value={data.services.length} />
            <Metric
              label="Applications"
              value={new Set(data.bindings.map((item) => item.app)).size}
            />
            <Metric label="Backup artifacts" value={data.backups.length} />
          </div>

          <div className="database-section-head">
            <div>
              <h2>MySQL &amp; PostgreSQL</h2>
              <p>
                Live sizes are loaded from each managed service. Backups are compressed logical
                dumps.
              </p>
            </div>
          </div>
          {data.services.length ? (
            <div className="database-service-list">
              {data.services.map((service) => (
                <DatabaseManager key={service.service} service={service} data={data} />
              ))}
            </div>
          ) : (
            <article className="panel full database-empty-panel">
              <EmptyPanel>No managed MySQL or PostgreSQL services.</EmptyPanel>
              <Button asChild>
                <Link href="/applications">Add from an application</Link>
              </Button>
            </article>
          )}

          <div className="database-section-head">
            <div>
              <h2>SQLite &amp; Litestream</h2>
              <p>Application-local files and their continuous replication status.</p>
            </div>
          </div>
          <article className="panel full">
            {fileBindings.length ? (
              <div className="table-wrap">
                <Table className="min-w-[650px] bg-card">
                  <TableHeader>
                    <TableRow>
                      <TableHead>Application</TableHead>
                      <TableHead>Engine</TableHead>
                      <TableHead>File</TableHead>
                      <TableHead>Recovery</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {fileBindings.flatMap((binding) =>
                      binding.resources.map((resource) => (
                        <TableRow key={`${binding.app}:${binding.engine}:${resource}`}>
                          <TableCell>
                            <strong>{binding.app}</strong>
                          </TableCell>
                          <TableCell>
                            <Badge variant="outline">
                              {binding.engine === "litestream" ? "Litestream" : "SQLite"}
                            </Badge>
                          </TableCell>
                          <TableCell>
                            <code>{resource}</code>
                          </TableCell>
                          <TableCell>
                            {binding.engine === "litestream"
                              ? binding.backupVerifiedAt
                                ? `Verified ${formatDate(binding.backupVerifiedAt)}`
                                : "Replication not verified"
                              : "Logical backup available from the CLI"}
                          </TableCell>
                        </TableRow>
                      )),
                    )}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <EmptyPanel>No SQLite or Litestream databases.</EmptyPanel>
            )}
          </article>

          {data.sqliteBackup && (
            <article className="panel full database-policy-panel">
              <div>
                <h2>Litestream policy</h2>
                <p className="muted">
                  Continuous replication settings shared by Litestream databases.
                </p>
              </div>
              <div className="detail-list database-policy-details">
                <Detail label="Status" value={data.sqliteBackup.enabled ? "Enabled" : "Disabled"} />
                <Detail label="Destination" value={data.sqliteBackup.destination} />
                <Detail label="Sync interval" value={data.sqliteBackup.syncInterval} />
                <Detail
                  label="Snapshots"
                  value={`${data.sqliteBackup.snapshotInterval} · retain ${data.sqliteBackup.snapshotRetention}`}
                />
              </div>
            </article>
          )}

          {!relationalBindings.length && !fileBindings.length && (
            <p className="muted database-no-bindings">
              No databases are currently bound to applications.
            </p>
          )}
        </>
      )}
    </section>
  );
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
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

function formatDate(value: string) {
  return new Date(value).toLocaleString();
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
