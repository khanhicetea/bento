import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  CopyableCode,
  DomainError,
  DomainLoading,
  EmptyState,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useOperationMutation } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";

export function SystemPage() {
  const system = useQuery({ queryKey: keys.system, queryFn: ({ signal }) => api.system.status(signal) });
  if (system.isPending)
    return (
      <Page>
        <DomainLoading label="system" />
      </Page>
    );
  if (system.error)
    return (
      <Page>
        <DomainError message={messageOf(system.error)} onRetry={() => void system.refetch()} />
      </Page>
    );
  const status = system.data;
  return (
    <Page>
      <PageHeader title="System" description="Backend, Docker, stack identity, and retained durable data." />
      <Panel
        title="Backend and Docker"
        actions={
          <StateBadge
            state={status.dockerError ? "failed" : "healthy"}
            label={status.dockerError ? "Docker unavailable" : "Connected"}
          />
        }
      >
        <dl className="grid gap-3 text-sm sm:grid-cols-2">
          <Info label="Stack" value={status.stackName} />
          <Info label="Stack ID" value={<CopyableCode value={status.stackId} />} />
          <Info label="Root" value={<CopyableCode value={status.root} />} />
          <Info label="Backend" value={`${status.version} · started ${formatRelative(status.startedAt)}`} />
          <Info label="Docker" value={status.dockerError ?? `${status.dockerVersion} · API ${status.dockerApi}`} />
          <Info label="Architecture" value={status.arch} />
        </dl>
      </Panel>
      <RetainedData />
    </Page>
  );
}
function Info({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="m-0 mt-1 min-w-0">{value}</dd>
    </div>
  );
}
function RetainedData() {
  const query = useQuery({ queryKey: keys.retired, queryFn: ({ signal }) => api.retired.list(signal) });
  const [target, setTarget] = useState<T.RetiredApp | null>(null);
  const prune = useOperationMutation((confirm: string) => api.retired.prune(target?.appId ?? "", confirm));
  const pending = (query.data?.retired ?? []).filter((app) => !app.prunedAt);
  return (
    <Panel
      title="Retained data"
      description="Application removal retains durable data. Pruning is permanent and UIDs are never reclaimed."
    >
      {pending.length === 0 ? (
        <EmptyState title="No retained data" />
      ) : (
        <div className="grid gap-2">
          {pending.map((app) => (
            <div
              key={app.appId}
              className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm"
            >
              <div>
                <strong>{app.slug}</strong> · uid {app.uid} · retired{" "}
                <span title={app.retiredAt}>{formatRelative(app.retiredAt)}</span>
                <div className="mt-1 text-xs text-muted-foreground">
                  <code>{app.home}</code> · {app.sqliteFileIds.length} SQLite · {app.relational.length} relational
                  binding(s)
                </div>
              </div>
              <Button size="sm" variant="destructive" onClick={() => setTarget(app)}>
                Prune…
              </Button>
            </div>
          ))}
        </div>
      )}
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={`Permanently prune ${target?.slug ?? "app"}?`}
        description={`Deletes retained home, SQLite files, and relational databases listed for this app. This cannot be undone.`}
        phrase="delete"
        destructive
        confirmLabel="Prune retained data"
        pending={prune.isPending}
        error={prune.error}
        onConfirm={(typed) => prune.mutate(typed, { onSuccess: () => setTarget(null) })}
      />
    </Panel>
  );
}
