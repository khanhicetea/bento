import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import {
  Cell,
  CopyableCode,
  DomainError,
  DomainLoading,
  KeyValues,
  PageHeader,
  StateBadge,
} from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useOperationMutation } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";

export function SystemPage() {
  const system = useQuery({ queryKey: keys.system, queryFn: ({ signal }) => api.system.status(signal) });
  if (system.isPending) return <DomainLoading label="system" />;
  if (system.error) return <DomainError message={messageOf(system.error)} onRetry={() => void system.refetch()} />;
  const status = system.data;
  return (
    <>
      <PageHeader title="System" description={status.stackName} />
      <div className="box box--3">
        <Cell>
          <div className="metric">
            <strong className="text-xl!">{status.version}</strong>
            <span>Bento · up {formatRelative(status.startedAt).replace(/ ago$/, "")}</span>
          </div>
        </Cell>
        <Cell className={status.dockerError ? "cell--alert" : ""}>
          <div className="metric">
            <strong className="text-xl!">{status.dockerError ? "Down" : status.dockerVersion}</strong>
            <span>Docker{status.dockerError ? "" : ` · API ${status.dockerApi}`}</span>
          </div>
        </Cell>
        <Cell>
          <div className="metric">
            <strong className="text-xl!">{status.arch}</strong>
            <span>Architecture</span>
          </div>
        </Cell>
        <Cell
          title="Stack"
          className="cell--wide"
          action={
            <StateBadge
              state={status.dockerError ? "failed" : "healthy"}
              label={status.dockerError ? "Docker down" : "Connected"}
            />
          }
        >
          <KeyValues
            items={[
              ["Name", status.stackName],
              ["ID", <CopyableCode value={status.stackId} />],
              ["Root", <CopyableCode value={status.root} />],
              ...(status.dockerError
                ? ([["Docker", <span className="text-destructive">{status.dockerError}</span>]] as Array<
                    [string, React.ReactNode]
                  >)
                : []),
            ]}
          />
        </Cell>
      </div>
      <RetainedData />
    </>
  );
}

function RetainedData() {
  const query = useQuery({ queryKey: keys.retired, queryFn: ({ signal }) => api.retired.list(signal) });
  const [target, setTarget] = useState<T.RetiredApp | null>(null);
  const prune = useOperationMutation((confirm: string) => api.retired.prune(target?.appId ?? "", confirm));
  const pending = (query.data?.retired ?? []).filter((app) => !app.prunedAt);
  return (
    <div className="box">
      <Cell title="Retained data">
        {query.error ? (
          <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
        ) : pending.length === 0 ? (
          <p className="note">Nothing retained</p>
        ) : (
          <div className="rows rows--lined">
            {pending.map((app) => (
              <div key={app.appId} className="row">
                <span className="mono">{app.slug.slice(0, 1).toUpperCase()}</span>
                <span className="row__main">
                  <strong>
                    {app.slug} <span className="font-normal text-muted-foreground">· uid {app.uid}</span>
                  </strong>
                  <small>
                    {app.home} · {app.sqliteFileIds.length} SQLite · {app.relational.length} DB
                  </small>
                </span>
                <span className="row__meta max-sm:hidden" title={app.retiredAt}>
                  {formatRelative(app.retiredAt)}
                </span>
                <Button size="sm" variant="ghost" className="text-destructive" onClick={() => setTarget(app)}>
                  <Trash2 /> Prune
                </Button>
              </div>
            ))}
          </div>
        )}
      </Cell>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={`Prune ${target?.slug ?? "app"}?`}
        description="Permanently deletes its home, SQLite files and databases. UIDs are never reused."
        phrase="delete"
        destructive
        confirmLabel="Prune forever"
        pending={prune.isPending}
        error={prune.error}
        onConfirm={(typed) => prune.mutate(typed, { onSuccess: () => setTarget(null) })}
      />
    </div>
  );
}
