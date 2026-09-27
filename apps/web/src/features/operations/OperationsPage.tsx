import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "wouter";
import { api, messageOf } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  EmptyState,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
import { describeOp, formatDuration, formatRelative } from "../../lib/format.ts";
import { isTerminal } from "./OperationTracker.tsx";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

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
      (state === "all" || op.state === state) &&
      `${op.kind} ${op.targetId} ${op.origin}`.toLowerCase().includes(search.toLowerCase()),
  );
  return (
    <Page wide>
      <PageHeader title="Activity" description="Durable runtime operations and their event timelines." />
      <div className="mb-4 flex flex-wrap gap-2">
        <Input
          className="max-w-sm"
          placeholder="Filter kind or target"
          value={search}
          onChange={(event) => setSearch(event.target.value)}
        />
        <NativeSelect className="w-40" value={state} onChange={(event) => setState(event.target.value)}>
          <option value="all">All states</option>
          {["queued", "running", "succeeded", "failed", "cancelled", "interrupted"].map((value) => (
            <option key={value}>{value}</option>
          ))}
        </NativeSelect>
      </div>
      {query.isPending && <DomainLoading label="operations" />}
      {query.error && <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />}
      {query.data && (
        <section className="overflow-hidden rounded-xl border bg-card">
          {operations.length === 0 ? (
            <EmptyState title="No matching operations" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>State</TableHead>
                    <TableHead>Operation</TableHead>
                    <TableHead>Target</TableHead>
                    <TableHead>Origin</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Duration</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {operations.map((op) => (
                    <TableRow key={op.id}>
                      <TableCell>
                        <StateBadge state={op.state} />
                      </TableCell>
                      <TableCell>
                        <Link href={`/activity/${op.id}`} className="font-medium hover:underline">
                          {describeOp(op)}
                        </Link>
                        {op.errorMessage && (
                          <div className="max-w-sm truncate text-xs text-destructive">{op.errorMessage}</div>
                        )}
                      </TableCell>
                      <TableCell>
                        <code className="text-xs">{op.targetId}</code>
                      </TableCell>
                      <TableCell>{op.origin}</TableCell>
                      <TableCell title={op.createdAt}>{formatRelative(op.createdAt)}</TableCell>
                      <TableCell>{formatDuration(op.startedAt, op.finishedAt)}</TableCell>
                    </TableRow>
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
  if (query.isPending)
    return (
      <Page>
        <DomainLoading label="operation" />
      </Page>
    );
  if (query.error)
    return (
      <Page>
        <DomainError message={messageOf(query.error)} />
      </Page>
    );
  const op = query.data;
  return (
    <Page>
      <PageHeader title={describeOp(op)} description={`Operation ${op.id}`} actions={<StateBadge state={op.state} />} />
      {op.guidance && (
        <Alert className="mb-4">
          <strong>Guidance:</strong> {op.guidance}
        </Alert>
      )}
      {op.errorMessage && (
        <Alert variant="destructive" className="mb-4">
          {op.errorMessage}
        </Alert>
      )}
      <Panel title="Timeline" description={`${op.origin} · ${formatDuration(op.startedAt, op.finishedAt)}`}>
        {(op.events ?? []).length === 0 ? (
          <EmptyState title="No events recorded yet" />
        ) : (
          <ol className="m-0 grid list-none gap-0 p-0">
            {(op.events ?? []).map((event) => (
              <li key={event.seq} className="grid grid-cols-[5rem_1fr] gap-3 border-l-2 py-2 pl-4 text-sm">
                <time className="text-xs text-muted-foreground" title={event.at}>
                  {event.at.slice(11, 19)}
                </time>
                <span>
                  <strong className={event.level === "error" ? "text-destructive" : ""}>{event.level}</strong> ·{" "}
                  {event.message}
                </span>
              </li>
            ))}
          </ol>
        )}
        {!isTerminal(op.state) && (
          <Button className="mt-4" variant="outline" disabled={cancel.isPending} onClick={() => cancel.mutate()}>
            Request cancellation
          </Button>
        )}
        {cancel.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(cancel.error)}
          </Alert>
        )}
      </Panel>
    </Page>
  );
}
