import { useQuery } from "@tanstack/react-query";
import { Link } from "wouter";
import type { ReactNode } from "react";
import { Archive, Database, HardDrive, Server } from "lucide-react";
import { DomainError, DomainLoading, EmptyPanel, StackNotReady } from "../../components/DomainState.tsx";
import { orpc } from "../../api/client.ts";
import { DatabaseManager } from "./DatabaseManager.tsx";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Spinner } from "@/components/ui/spinner";

export function DatabasesPage() {
  const query = useQuery(orpc.data.overview.queryOptions({ input: {} }));
  const data = query.data;

  if (!data && query.isPending) {
    return (
      <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4">
        <DomainLoading label="databases" />
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

  const fileBindings = data.bindings.filter(
    (binding) => binding.engine === "sqlite" || binding.engine === "litestream",
  );
  const databaseCount = data.bindings.reduce((total, binding) => total + binding.resources.length, 0);
  const fileDatabaseCount = fileBindings.reduce((total, binding) => total + binding.resources.length, 0);

  return (
    <section className="mx-auto w-full max-w-[1800px] p-[clamp(1rem,2.5vw,2.5rem)] max-[760px]:p-4" aria-live="polite">
      <div className="flex items-end justify-between gap-6 max-[760px]:items-stretch max-[760px]:flex-col">
        <div>
          <p className="mb-2 text-[0.68rem] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
            Control plane / Data
          </p>
          <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">Databases</h2>
          <p className="m-0 mt-2 max-w-[680px] text-sm text-muted-foreground">
            Monitor database services, create backups, and recover application data from one place.
          </p>
        </div>
        <Button
          className="max-[760px]:w-full"
          variant="outline"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {query.isFetching && <Spinner />}
          Refresh inventory
        </Button>
      </div>

      {!data.initialized ? (
        <div className="mt-8">
          <StackNotReady stackRoot={data.stackRoot} error={data.error} />
        </div>
      ) : (
        <>
          <div className="mt-8 grid grid-cols-4 gap-3 max-[1050px]:grid-cols-2 max-[560px]:grid-cols-1">
            <Summary value={databaseCount} label="Attached databases" icon={<Database className="size-4" />} />
            <Summary
              value={data.services.length}
              label="Managed services"
              icon={<Server className="size-4" />}
              tone="success"
            />
            <Summary
              value={new Set(data.bindings.map((item) => item.app)).size}
              label="Applications using data"
              icon={<HardDrive className="size-4" />}
            />
            <Summary value={data.backups.length} label="Backup artifacts" icon={<Archive className="size-4" />} />
          </div>

          <SectionHeading
            eyebrow="Managed services"
            title="MySQL & PostgreSQL"
            description="Live sizes are loaded from each service. Backups are compressed logical dumps."
            count={data.services.length}
            itemLabel="service"
            icon={<Server className="size-4" />}
          />
          {data.services.length ? (
            <div className="mt-4 grid gap-5">
              {data.services.map((service) => (
                <DatabaseManager key={service.service} service={service} data={data} />
              ))}
            </div>
          ) : (
            <article className="mt-4 flex items-center justify-between gap-4 rounded-2xl border border-dashed border-border bg-card px-6 py-8 text-card-foreground shadow-sm max-[760px]:items-stretch max-[760px]:flex-col">
              <EmptyPanel>No managed MySQL or PostgreSQL services.</EmptyPanel>
              <Button asChild>
                <Link href="/applications">Add from an application</Link>
              </Button>
            </article>
          )}

          <SectionHeading
            eyebrow="Application-local storage"
            title="SQLite & Litestream"
            description="Application-local files and their continuous replication status."
            count={fileDatabaseCount}
            itemLabel="database"
            icon={<HardDrive className="size-4" />}
          />
          <article className="mt-4 overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-sm">
            {fileDatabaseCount ? (
              <div className="overflow-auto">
                <Table className="min-w-[720px] bg-card">
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
                            <div className="flex items-center gap-2">
                              <span className="grid size-7 shrink-0 place-items-center rounded-lg bg-muted text-muted-foreground">
                                <Database className="size-3.5" aria-hidden="true" />
                              </span>
                              <strong>{binding.app}</strong>
                            </div>
                          </TableCell>
                          <TableCell>
                            <Badge variant="outline">{binding.engine === "litestream" ? "Litestream" : "SQLite"}</Badge>
                          </TableCell>
                          <TableCell>
                            <code className="text-xs">{resource}</code>
                          </TableCell>
                          <TableCell>
                            {binding.engine === "litestream" ? (
                              binding.backupVerifiedAt ? (
                                <span className="text-sm text-emerald-700 dark:text-emerald-300">
                                  Verified {formatDate(binding.backupVerifiedAt)}
                                </span>
                              ) : (
                                <span className="text-sm text-amber-700 dark:text-amber-300">
                                  Replication not verified
                                </span>
                              )
                            ) : (
                              <span className="text-sm text-muted-foreground">
                                Logical backup available from the CLI
                              </span>
                            )}
                          </TableCell>
                        </TableRow>
                      )),
                    )}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <div className="p-6">
                <EmptyPanel>No SQLite or Litestream databases.</EmptyPanel>
              </div>
            )}
          </article>

          {data.sqliteBackup && (
            <article className="mt-5 grid grid-cols-[minmax(12rem,0.7fr)_minmax(0,1.3fr)] gap-8 rounded-2xl border border-border bg-card p-6 text-card-foreground shadow-sm max-[900px]:grid-cols-1">
              <div>
                <div className="mb-3 flex size-10 items-center justify-center rounded-xl bg-muted text-muted-foreground">
                  <Archive className="size-5" aria-hidden="true" />
                </div>
                <h3 className="m-0 text-base font-semibold">Litestream policy</h3>
                <p className="m-0 mt-2 text-sm text-muted-foreground">
                  Continuous replication settings shared by Litestream databases.
                </p>
              </div>
              <div className="grid rounded-xl border border-border bg-muted/30 px-4">
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
        </>
      )}
    </section>
  );
}

function SectionHeading({
  eyebrow,
  title,
  description,
  count,
  itemLabel,
  icon,
}: {
  eyebrow: string;
  title: string;
  description: string;
  count: number;
  itemLabel: string;
  icon: ReactNode;
}) {
  return (
    <div className="mt-10 flex items-end justify-between gap-4 max-[760px]:items-start">
      <div className="flex min-w-0 items-start gap-3">
        <span className="mt-1 grid size-9 shrink-0 place-items-center rounded-xl bg-muted text-muted-foreground">
          {icon}
        </span>
        <div className="min-w-0">
          <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
            {eyebrow}
          </p>
          <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">{title}</h3>
          <p className="m-0 mt-1 text-sm text-muted-foreground">{description}</p>
        </div>
      </div>
      <Badge variant="secondary" className="shrink-0">
        {count} {count === 1 ? itemLabel : `${itemLabel}s`}
      </Badge>
    </div>
  );
}

function Summary({
  value,
  label,
  icon,
  tone = "default",
}: {
  value: number;
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

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 border-b border-border py-2.5 text-[0.84rem] last:border-b-0">
      <span className="text-muted-foreground">{label}</span>
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
