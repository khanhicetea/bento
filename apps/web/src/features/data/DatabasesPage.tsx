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
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="databases" />
      </section>
    );
  }
  if (!data && query.error) {
    return (
      <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
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
    <section className="w-full max-w-[1800px] mx-auto p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
      <div className="mb-4 flex items-end justify-between gap-4 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <h2>Databases</h2>
          <p className="m-0 my-1 opacity-60">
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
          <div className="my-[1.2rem] grid grid-cols-4 gap-4 max-[1050px]:grid-cols-2 max-[760px]:grid-cols-1">
            <Metric label="Databases" value={databaseCount} />
            <Metric label="Managed services" value={data.services.length} />
            <Metric
              label="Applications"
              value={new Set(data.bindings.map((item) => item.app)).size}
            />
            <Metric label="Backup artifacts" value={data.backups.length} />
          </div>

          <div className="my-8 mb-3.5 [&_h2]:m-0 [&_p]:m-0 [&_p]:mt-1 [&_p]:opacity-65">
            <div>
              <h2>MySQL &amp; PostgreSQL</h2>
              <p>
                Live sizes are loaded from each managed service. Backups are compressed logical
                dumps.
              </p>
            </div>
          </div>
          {data.services.length ? (
            <div className="mt-5 grid gap-5">
              {data.services.map((service) => (
                <DatabaseManager key={service.service} service={service} data={data} />
              ))}
            </div>
          ) : (
            <article className="col-span-full flex items-center justify-between gap-4 rounded-xl border border-border bg-card p-5 text-card-foreground max-[760px]:items-stretch max-[760px]:flex-col">
              <EmptyPanel>No managed MySQL or PostgreSQL services.</EmptyPanel>
              <Button asChild>
                <Link href="/applications">Add from an application</Link>
              </Button>
            </article>
          )}

          <div className="my-8 mb-3.5 [&_h2]:m-0 [&_p]:m-0 [&_p]:mt-1 [&_p]:opacity-65">
            <div>
              <h2>SQLite &amp; Litestream</h2>
              <p>Application-local files and their continuous replication status.</p>
            </div>
          </div>
          <article className="col-span-full rounded-xl border border-border bg-card p-5 text-card-foreground [&_h2]:mt-0 [&_h2]:mb-3.5 [&_h3]:mt-0 [&_h3]:mb-2">
            {fileBindings.length ? (
              <div className="overflow-auto rounded-[0.8rem] border border-border">
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
            <article className="grid grid-cols-[minmax(12rem,0.7fr)_minmax(0,1.3fr)] gap-8 rounded-xl border border-border bg-card p-5 text-card-foreground max-[900px]:grid-cols-1">
              <div>
                <h2>Litestream policy</h2>
                <p className="text-sm opacity-60">
                  Continuous replication settings shared by Litestream databases.
                </p>
              </div>
              <div className="grid">
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
            <p className="text-center text-sm opacity-60">
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
    <div className="rounded-xl border border-border bg-card p-5 text-card-foreground">
      <div className="text-xs uppercase tracking-[0.06em] opacity-60">{label}</div>
      <div className="mt-1 text-3xl font-bold">{value}</div>
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

function formatDate(value: string) {
  return new Date(value).toLocaleString();
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
