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
  KeyValues,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { describeOp, formatDuration, formatRelative } from "../../lib/format.ts";
import { isTerminal } from "./OperationTracker.tsx";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const stateFilters: Array<[string, string]> = [
  ["all", "All"],
  ["running", "Running"],
  ["succeeded", "Done"],
  ["failed", "Failed"],
];

export function OperationsPage() {
  const query = useQuery({
    queryKey: keys.operations.list(),
    queryFn: ({ signal }) => api.operations.list(undefined, signal),
    refetchInterval: 5_000,
  });
  const [state, setState] = useState("all");
  const [search, setSearch] = useState("");
  const operations = (query.data?.operations ?? []).filter(
    (op) =>
      (state === "all" || op.state === state || (state === "running" && op.state === "queued")) &&
      `${op.kind} ${op.targetId} ${op.origin}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <>
      <PageHeader title="Activity" />
      <div className="toolbar">
        <label className="toolbar__search">
          <Search aria-hidden="true" />
          <span className="sr-only">Search activity</span>
          <Input placeholder="Search" value={search} onChange={(event) => setSearch(event.target.value)} />
        </label>
        <div className="seg" role="group" aria-label="Filter by state">
          {stateFilters.map(([value, label]) => (
            <button key={value} type="button" aria-pressed={state === value} onClick={() => setState(value)}>
              {label}
            </button>
          ))}
        </div>
      </div>
      {query.isPending && <DomainLoading label="activity" />}
      {query.error && <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />}
      {query.data && (
        <div className="box">
          <div className="cell">
            {operations.length === 0 ? (
              <EmptyState icon={<Activity />} title="Nothing here" />
            ) : (
              <div className="rows rows--lined">
                {operations.map((op) => (
                  <Link key={op.id} href={`/activity/${op.id}`} className="row">
                    <span className="row__main">
                      <strong>{describeOp(op)}</strong>
                      <small className={op.errorMessage ? "text-destructive!" : ""}>
                        {op.errorMessage || `${op.origin} · ${formatDuration(op.startedAt, op.finishedAt)}`}
                      </small>
                    </span>
                    <span className="row__meta max-sm:hidden" title={op.createdAt}>
                      {formatRelative(op.createdAt)}
                    </span>
                    <StateBadge state={op.state} />
                  </Link>
                ))}
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}

export function OperationDetailPage({ id }: { id: string }) {
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
  if (query.isPending) return <DomainLoading label="operation" />;
  if (query.error) return <DomainError message={messageOf(query.error)} />;
  const op = query.data;
  return (
    <>
      <PageHeader
        back={{ href: "/activity", label: "Activity" }}
        title={describeOp(op)}
        actions={
          !isTerminal(op.state) && (
            <Button variant="outline" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
              Cancel
            </Button>
          )
        }
      />
      <div className="box box--main">
        <Cell title="Timeline">
          {(op.events ?? []).length === 0 ? (
            <p className="note">No events yet</p>
          ) : (
            <ol className="timeline">
              {(op.events ?? []).map((event) => (
                <li key={event.seq} data-level={event.level}>
                  <time title={event.at}>{event.at.slice(11, 19)}</time>
                  <span className={event.level === "error" ? "text-destructive" : ""}>{event.message}</span>
                </li>
              ))}
            </ol>
          )}
        </Cell>
        <div className="col">
          <Cell title="Details">
            <KeyValues
              items={[
                ["State", <StateBadge state={op.state} />],
                ["Origin", op.origin],
                ["Duration", formatDuration(op.startedAt, op.finishedAt)],
                ["Created", <span title={op.createdAt}>{formatRelative(op.createdAt)}</span>],
                ["ID", <code className="note">{op.id}</code>],
              ]}
            />
          </Cell>
          {(op.errorMessage || op.guidance || cancel.error) && (
            <Cell title="Notes" className={op.errorMessage || cancel.error ? "cell--alert" : ""}>
              <div className="grid gap-2 text-sm">
                {op.errorMessage && <p className="text-destructive">{op.errorMessage}</p>}
                {cancel.error && <p className="text-destructive">{messageOf(cancel.error)}</p>}
                {op.guidance && <p>{op.guidance}</p>}
              </div>
            </Cell>
          )}
        </div>
      </div>
    </>
  );
}
