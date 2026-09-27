import { useState } from "react";
import { AppWindow, ExternalLink, Search } from "lucide-react";
import { Link } from "wouter";
import { messageOf, type T } from "../../api/client.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { DomainError, DomainLoading, EmptyState, Page, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { useActiveOperations } from "../operations/useActiveOperations.ts";
import { useAppAction, useApplicationList, type AppAction } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

type Filter = "all" | "running" | "stopped" | "attention";

export function ApplicationsPage() {
  const list = useApplicationList();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const needle = query.trim().toLowerCase();
  const apps = (list.data?.apps ?? []).filter((app) => {
    const matchesText = `${app.slug} ${app.primaryDomain}`.toLowerCase().includes(needle);
    const drift =
      app.desiredRuntime !==
      (app.observed.state === "healthy" || app.observed.state === "starting" ? "running" : "stopped");
    const matchesFilter =
      filter === "all" ||
      (filter === "running" && app.desiredRuntime === "running" && !drift) ||
      (filter === "stopped" && app.desiredRuntime === "stopped" && !drift) ||
      (filter === "attention" &&
        (drift ||
          app.observed.state === "blocked" ||
          app.observed.state === "failed" ||
          app.observed.state === "unhealthy"));
    return matchesText && matchesFilter;
  });
  return (
    <Page wide>
      <PageHeader
        title="Applications"
        description="Runtime, routing, and current activity at a glance."
        actions={
          <Button asChild>
            <Link href="/apps/new">New application</Link>
          </Button>
        }
      />
      {list.isPending && <DomainLoading label="applications" />}
      {list.error && <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />}
      {list.data && (
        <section className="overflow-hidden rounded-xl border bg-card shadow-sm">
          <div className="flex flex-wrap items-center gap-2 border-b p-3">
            <div className="relative min-w-56 flex-1 sm:max-w-sm">
              <Search className="absolute top-2.5 left-3 size-4 text-muted-foreground" />
              <Input
                className="pl-9"
                placeholder="Search slug or domain"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <div className="flex flex-wrap gap-1" aria-label="Filter applications">
              {(["all", "running", "stopped", "attention"] as const).map((value) => (
                <Button
                  key={value}
                  size="sm"
                  variant={filter === value ? "default" : "outline"}
                  onClick={() => setFilter(value)}
                >
                  {value === "attention" ? "Needs attention" : value[0]?.toUpperCase() + value.slice(1)}
                </Button>
              ))}
            </div>
          </div>
          {apps.length === 0 ? (
            <EmptyState
              icon={<AppWindow className="size-8" />}
              title={list.data.apps.length === 0 ? "No applications yet" : "No matching applications"}
              body={
                list.data.apps.length === 0
                  ? "Create an application to provision its isolated runtime."
                  : "Adjust the search or status filter."
              }
              action={
                list.data.apps.length === 0 ? (
                  <Button asChild>
                    <Link href="/apps/new">Create application</Link>
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Runtime</TableHead>
                    <TableHead>Ingress</TableHead>
                    <TableHead>Activity</TableHead>
                    <TableHead>
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {apps.map((app) => (
                    <ApplicationRow key={app.id} app={app} />
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </section>
      )}
    </Page>
  );
}

function ApplicationRow({ app }: { app: T.AppSummary }) {
  const action = useAppAction();
  const active = useActiveOperations(app.id);
  const [confirm, setConfirm] = useState<AppAction | null>(null);
  const observedRunning = app.observed.state === "healthy" || app.observed.state === "starting";
  const drift = app.desiredRuntime !== (observedRunning ? "running" : "stopped");
  const status = drift ? "drift" : app.observed.state;
  const run = (verb: AppAction) => action.mutate({ id: app.id, action: verb }, { onSuccess: () => setConfirm(null) });
  const requiresConfirm = (verb: AppAction) => verb === "stop" || verb === "restart" || verb === "unpublish";
  const request = (verb: AppAction) => (requiresConfirm(verb) ? setConfirm(verb) : run(verb));
  return (
    <>
      <TableRow>
        <TableCell>
          <Link
            className="font-semibold text-foreground hover:underline"
            href={`/apps/${encodeURIComponent(app.slug)}`}
          >
            {app.slug}
          </Link>
          {app.primaryDomain && (
            <a
              className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground hover:underline"
              href={`//${app.primaryDomain}`}
              target="_blank"
              rel="noreferrer"
            >
              {app.primaryDomain}
              <ExternalLink className="size-3" />
            </a>
          )}
        </TableCell>
        <TableCell>
          <StateBadge
            state={status}
            title={app.observed.message}
            label={drift ? `Drift: wants ${app.desiredRuntime}, is ${app.observed.state}` : undefined}
          />
        </TableCell>
        <TableCell>
          <span className="font-mono text-xs">
            {app.toolchain} {app.version}
          </span>
        </TableCell>
        <TableCell>
          {app.ingress === "managed"
            ? `${app.publication === "published" ? "Published" : "Unpublished"} · managed`
            : app.ingress === "none"
              ? "Private"
              : "External"}
        </TableCell>
        <TableCell>
          {active.active ? (
            <StateBadge state="running" label={active.operations[0]?.kind.replace("app.", "") ?? "Working"} />
          ) : (
            <span className="text-muted-foreground">—</span>
          )}
        </TableCell>
        <TableCell>
          <div className="flex justify-end gap-1">
            <Button
              size="xs"
              disabled={active.active || action.isPending}
              onClick={() => request(app.desiredRuntime === "stopped" ? "start" : "restart")}
            >
              {app.desiredRuntime === "stopped" ? "Start" : "Restart"}
            </Button>
            {app.desiredRuntime === "running" && (
              <Button
                size="xs"
                variant="outline"
                disabled={active.active || action.isPending}
                onClick={() => request("stop")}
              >
                Stop
              </Button>
            )}
            {app.ingress === "managed" && app.desiredRuntime === "running" && (
              <Button
                size="xs"
                variant="outline"
                disabled={active.active || action.isPending}
                onClick={() => request(app.publication === "published" ? "unpublish" : "publish")}
              >
                {app.publication === "published" ? "Unpublish" : "Publish"}
              </Button>
            )}
          </div>
          {action.error && (
            <Alert variant="destructive" className="mt-2 max-w-xs">
              {messageOf(action.error)}
            </Alert>
          )}
        </TableCell>
      </TableRow>
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={`${confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Change"} ${app.slug}?`}
        description={`${confirm === "stop" ? "This stops the running application." : confirm === "unpublish" ? "This removes its managed public route." : "This replaces the running instance after a controlled stop."}`}
        confirmLabel={confirm ? confirm[0]?.toUpperCase() + confirm.slice(1) : "Confirm"}
        pending={action.isPending}
        error={action.error}
        onConfirm={() => confirm && run(confirm)}
      />
    </>
  );
}
