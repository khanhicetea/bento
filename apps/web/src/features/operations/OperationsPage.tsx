import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { EmptyPanel, Field, Page, PageHeader, Panel, StateBadge } from "../../components/DomainState.tsx";
import { useOperationMutation } from "../applications/useApplications.ts";
import { isTerminal } from "./OperationTracker.tsx";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function OperationsPage() {
  const system = useQuery({ queryKey: keys.system, queryFn: ({ signal }) => api.system.status(signal) });
  const ops = useQuery({
    queryKey: keys.operations.list(),
    queryFn: ({ signal }) => api.operations.list(undefined, signal),
    refetchInterval: 5_000,
  });
  const [open, setOpen] = useState<string | null>(null);
  return (
    <Page>
      <PageHeader
        section="Operations"
        title="Operations"
        description="Every runtime change is a durable operation. Interrupted operations are never replayed automatically."
      />
      {system.data && (
        <Panel title="Backend">
          <div className="grid grid-cols-3 gap-3 text-sm max-[760px]:grid-cols-1">
            <span>
              Stack <strong>{system.data.stackName}</strong> <code className="text-xs">{system.data.stackId}</code>
            </span>
            <span>
              Root <code className="text-xs">{system.data.root}</code>
            </span>
            <span>
              Backend {system.data.version} since {system.data.startedAt}
            </span>
            <span>
              Docker {system.data.dockerVersion || "unavailable"} · API {system.data.dockerApi} · {system.data.arch}
            </span>
            <span>
              {system.data.apps} apps · {system.data.runningApps} desired running
            </span>
            <span>{system.data.queuedOps} active operation(s)</span>
          </div>
        </Panel>
      )}
      <Panel title="Recent operations">
        {(ops.data?.operations ?? []).length === 0 ? (
          <EmptyPanel>No operations yet.</EmptyPanel>
        ) : (
          <div className="grid">
            {ops.data?.operations.map((op) => (
              <div key={op.id} className="border-t border-border py-2 text-sm first:border-0">
                <button
                  className="flex w-full items-center gap-3 text-left"
                  onClick={() => setOpen(open === op.id ? null : op.id)}
                >
                  <StateBadge state={op.state} />
                  <span className="font-medium">{op.kind}</span>
                  <span className="text-muted-foreground">{op.targetId}</span>
                  <span className="ml-auto text-xs text-muted-foreground">
                    {op.origin} · {op.createdAt}
                  </span>
                </button>
                {op.errorMessage && <div className="mt-1 text-xs text-destructive">{op.errorMessage}</div>}
                {open === op.id && <OperationDetail id={op.id} />}
              </div>
            ))}
          </div>
        )}
      </Panel>
      <RetiredPanel />
    </Page>
  );
}

function OperationDetail({ id }: { id: string }) {
  const queryClient = useQueryClient();
  const q = useQuery({
    queryKey: keys.operations.detail(id),
    queryFn: ({ signal }) => api.operations.get(id, signal),
    refetchInterval: (query) => (isTerminal(query.state.data?.state) ? false : 1000),
  });
  const cancel = useMutation({
    mutationFn: () => api.operations.cancel(id),
    onSuccess: (op) => queryClient.setQueryData(keys.operations.detail(id), op),
  });
  if (!q.data) return null;
  return (
    <div className="mt-2 rounded-md bg-muted/40 p-3 text-xs">
      {q.data.guidance && (
        <p className="m-0 mb-2">
          <strong>Guidance:</strong> {q.data.guidance}
        </p>
      )}
      {(q.data.events ?? []).map((e) => (
        <div key={e.seq}>
          <span className="text-muted-foreground">{e.at.slice(11, 19)}</span> [{e.level}] {e.message}
        </div>
      ))}
      {!isTerminal(q.data.state) && (
        <Button size="xs" variant="outline" className="mt-2" onClick={() => cancel.mutate()}>
          Request cancel (honored at a safe boundary)
        </Button>
      )}
    </div>
  );
}

function RetiredPanel() {
  const q = useQuery({ queryKey: keys.retired, queryFn: ({ signal }) => api.retired.list(signal) });
  const [target, setTarget] = useState<T.RetiredApp | null>(null);
  const [confirm, setConfirm] = useState("");
  const prune = useOperationMutation(() => api.retired.prune(target?.appId ?? "", confirm));
  const pending = (q.data?.retired ?? []).filter((r) => !r.prunedAt);
  return (
    <Panel
      title="Retained data of removed apps"
      description="Removal retains durable data. Pruning permanently deletes it; UIDs are never reclaimed."
    >
      {pending.length === 0 ? (
        <EmptyPanel>No retained data.</EmptyPanel>
      ) : (
        pending.map((r) => (
          <div key={r.appId} className="border-t border-border py-2 text-sm first:border-0">
            <div className="flex items-center justify-between">
              <span>
                <strong>{r.slug}</strong> · uid {r.uid} · retired {r.retiredAt}
              </span>
              <Button
                size="xs"
                variant="destructive"
                onClick={() => {
                  setTarget(r);
                  setConfirm("");
                }}
              >
                Prune…
              </Button>
            </div>
            {target?.appId === r.appId && (
              <div className="mt-2 grid gap-2 rounded-md border border-destructive/40 p-3 text-xs">
                <div>
                  Home: <code>{r.home}</code>
                </div>
                {r.sqliteFileIds.map((id) => (
                  <div key={id}>
                    SQLite directory: <code>{id}</code>
                  </div>
                ))}
                {r.relational.map((rel) => (
                  <div key={rel.service}>
                    {rel.engine} on {rel.service}: user {rel.username}, databases {rel.databases.join(", ")}
                  </div>
                ))}
                <Field label='Type "delete" to permanently delete everything listed'>
                  <Input value={confirm} onChange={(e) => setConfirm(e.target.value)} />
                </Field>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={confirm !== "delete" || prune.isPending}
                  onClick={() => prune.mutate(undefined, { onSuccess: () => setTarget(null) })}
                >
                  Prune {r.slug}
                </Button>
                {prune.error && <Alert variant="destructive">{messageOf(prune.error)}</Alert>}
              </div>
            )}
          </div>
        ))
      )}
    </Panel>
  );
}
