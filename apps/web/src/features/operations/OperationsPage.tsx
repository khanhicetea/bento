import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, Search } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  Cell,
  DomainError,
  DomainLoading,
  EmptyState,
  moodOf,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { Mascot } from "../../components/Mascot.tsx";
import { describeOp, formatDuration, formatRelative } from "../../lib/format.ts";
import { isTerminal } from "./OperationTracker.tsx";
import { useApplicationList } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const stateFilters: Array<[string, string]> = [
  ["all", "All"],
  ["running", "Active"],
  ["failed", "Failed"],
  ["succeeded", "Done"],
];
const matchesState = (filter: string, state: string) =>
  filter === "all" || state === filter || (filter === "running" && state === "queued");

export function OperationsPage({ selectedId }: { selectedId?: string }) {
  const query = useQuery({
    queryKey: keys.operations.list(),
    queryFn: ({ signal }) => api.operations.list(undefined, signal),
    refetchInterval: 5_000,
  });
  const [state, setState] = useState("all");
  const [search, setSearch] = useState("");
  const all = query.data?.operations ?? [];
  const operations = all.filter(
    (op) =>
      matchesState(state, op.state) &&
      `${op.kind} ${op.targetId} ${op.origin}`.toLowerCase().includes(search.toLowerCase()),
  );
  const currentId = selectedId ?? operations[0]?.id;
  return (
    <>
      <PageHeader
        title={
          <>
            Activity <small className="font-mono text-sm text-muted-foreground">{all.length}</small>
          </>
        }
      />
      <div className="toolbar">
        <label className="toolbar__search">
          <Search aria-hidden="true" />
          <span className="sr-only">Search activity</span>
          <Input placeholder="Search" value={search} onChange={(event) => setSearch(event.target.value)} />
        </label>
        <div className="seg" role="group" aria-label="Filter by state">
          {stateFilters.map(([value, label]) => (
            <button
              key={value}
              type="button"
              aria-pressed={state === value}
              className={value === "failed" && state !== value ? "text-destructive!" : undefined}
              onClick={() => setState(value)}
            >
              {label} <span className="font-mono">{all.filter((op) => matchesState(value, op.state)).length}</span>
            </button>
          ))}
        </div>
      </div>
      {query.isPending && <DomainLoading label="activity" />}
      {query.error && <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />}
      {query.data && (
        <section className="box box--activity" aria-label="Operations">
          <Cell title="Operations" icon={<Activity />} action={<span className="label text-xs">Newest first</span>}>
            {operations.length === 0 ? (
              <EmptyState title="Nothing here" />
            ) : (
              <div className="rows">
                {operations.map((op) => (
                  <Link
                    key={op.id}
                    href={`/activity/${op.id}`}
                    className="row op-row"
                    aria-current={op.id === currentId ? "true" : undefined}
                  >
                    <span className="row__main">
                      <strong>{describeOp(op)}</strong>
                      {op.errorMessage && <small className="text-destructive!">{op.errorMessage}</small>}
                    </span>
                    <StateBadge state={op.state} />
                    <span className="row__meta w-20 text-right max-sm:hidden" title={op.createdAt}>
                      {formatRelative(op.createdAt)}
                    </span>
                  </Link>
                ))}
              </div>
            )}
          </Cell>
          {currentId ? (
            <OperationDetail id={currentId} />
          ) : (
            <Cell kind="kara">
              <EmptyState title="No operation" />
            </Cell>
          )}
        </section>
      )}
    </>
  );
}

function OperationDetail({ id }: { id: string }) {
  const apps = useApplicationList();
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: keys.operations.detail(id),
    queryFn: ({ signal }) => api.operations.get(id, signal),
    refetchInterval: (current) => (isTerminal(current.state.data?.state) ? false : 1_000),
  });
  const cancel = useMutation({
    mutationFn: () => api.operations.cancel(id),
    onSuccess: (op) => queryClient.setQueryData(keys.operations.detail(id), op),
  });
  if (query.isPending)
    return (
      <Cell>
        <DomainLoading label="operation" />
      </Cell>
    );
  if (query.error)
    return (
      <Cell>
        <DomainError message={messageOf(query.error)} />
      </Cell>
    );
  const op = query.data;
  const events = op.events ?? [];
  const appSlug = apps.data?.apps.find((app) => app.id === op.targetId)?.slug;
  const kind = op.state === "failed" ? "ume" : isTerminal(op.state) ? "gohan" : "tamago";
  return (
    <Cell kind={kind} className="grid content-start gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="grid justify-items-start gap-2">
          <StateBadge state={op.state} />
          <h2 className="text-2xl font-semibold">{describeOp(op)}</h2>
        </div>
        <Mascot mood={moodOf(op.state)} size={84} />
      </div>
      <dl className="kv">
        {(
          [
            ["Origin", op.origin],
            ["Duration", formatDuration(op.startedAt, op.finishedAt)],
            ["Created", <span title={op.createdAt}>{formatRelative(op.createdAt)}</span>],
            [
              "ID",
              <code className="block truncate text-xs" title={op.id}>
                {op.id}
              </code>,
            ],
          ] as const
        ).map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {op.waitingOn && (
        <p className="notice notice--caution notice--plain">
          Queued behind{" "}
          <Link href={`/activity/${op.waitingOn}`} className="font-medium underline">
            an earlier operation
          </Link>{" "}
          on the same app, data service, or stack-wide state. It starts when that one finishes.
        </p>
      )}
      {(op.errorMessage || op.guidance || cancel.error) && (
        <div className="grid gap-2 rounded-[0.875rem] bg-[var(--ume)] p-4 text-sm">
          {op.errorMessage && <p className="font-medium text-[var(--ume-ink)]">{op.errorMessage}</p>}
          {cancel.error && <p className="text-destructive">{messageOf(cancel.error)}</p>}
          {op.guidance && <p>{op.guidance}</p>}
        </div>
      )}
      <section className="grid min-h-0 gap-2">
        <h3 className="label text-xs">
          Events <span className="font-mono text-muted-foreground">{events.length}</span>
        </h3>
        <div className="max-h-[50vh] overflow-y-auto">
          {events.length === 0 ? (
            <p className="note py-2">No events yet</p>
          ) : (
            <ol className="timeline">
              {events.map((event) => (
                <li key={event.seq} data-level={event.level}>
                  <time title={event.at}>{event.at.slice(11, 19)}</time>
                  <span className={event.level === "error" ? "text-destructive" : ""}>{event.message}</span>
                </li>
              ))}
            </ol>
          )}
        </div>
      </section>
      <div className="flex flex-wrap gap-2 border-t border-foreground/10 pt-3">
        {appSlug && (
          <Button variant="outline" asChild>
            <Link href={`/apps/${encodeURIComponent(appSlug)}`}>Open {appSlug} →</Link>
          </Button>
        )}
        {!isTerminal(op.state) && (
          <Button variant="danger" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            Cancel operation
          </Button>
        )}
      </div>
    </Cell>
  );
}
