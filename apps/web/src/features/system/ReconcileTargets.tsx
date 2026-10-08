import { useQuery } from "@tanstack/react-query";
import { Link } from "wouter";
import { RotateCw } from "lucide-react";
import { Mascot } from "../../components/Mascot.tsx";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Cell, DomainError, StateBadge } from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";

const fixedTargets: Record<string, string> = {
  edge: "Edge proxy",
  tunnel: "Tunnel",
  dbadmin: "Database browser",
};

/** Reconciler targets that are retrying, blocked, or have a repair queued. */
export function ReconcileTargets() {
  // The reconciler changes these outside tracked operations, so poll.
  const query = useQuery({
    queryKey: keys.reconcile,
    queryFn: ({ signal }) => api.system.reconcile(signal),
    refetchInterval: 15_000,
  });
  const apps = useQuery({ queryKey: keys.apps.list(), queryFn: ({ signal }) => api.apps.list(signal) });
  const slugs = new Map((apps.data?.apps ?? []).map((app) => [app.id, app.slug]));
  const label = (id: string) =>
    fixedTargets[id] ??
    (id.startsWith("service:") ? `Service ${id.slice("service:".length)}` : `App ${slugs.get(id) ?? id}`);
  const targets = query.data?.targets ?? [];
  return (
    <Cell
      title="Reconciliation"
      icon={<RotateCw />}
      kind={targets.length > 0 ? "tamago" : "gohan"}
      action={
        targets.length > 0 && (
          <span className="label text-xs text-[var(--tamago-ink)]">{targets.length} not converged</span>
        )
      }
    >
      {query.error ? (
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      ) : query.isPending ? (
        <p className="note">Loading…</p>
      ) : targets.length === 0 ? (
        <div className="flex items-center gap-3">
          <Mascot mood="ok" size={56} />
          <StateBadge state="healthy" label="All converged" />
        </div>
      ) : (
        <div className="rows rows--lined">
          {targets.map((target) => (
            <TargetRow key={target.id} target={target} label={label(target.id)} />
          ))}
        </div>
      )}
    </Cell>
  );
}

function TargetRow({ target, label }: { target: T.ReconcileTarget; label: string }) {
  const { failures, blocked, nextAttempt, lastError } = target.reconcile;
  const state = blocked ? "blocked" : target.pendingOperation ? "queued" : "failed";
  const badge = blocked ? "Blocked" : target.pendingOperation ? "Repairing" : "Retrying";
  return (
    <div className="row rounded-[0.75rem] bg-card">
      <span className="row__main">
        <strong>
          {label}{" "}
          <span className="font-normal text-muted-foreground">
            · {failures} failed {failures === 1 ? "attempt" : "attempts"}
          </span>
        </strong>
        <small className={lastError ? "text-destructive" : undefined}>
          {lastError ||
            (blocked
              ? "Re-apply its settings or start it to retry"
              : nextAttempt
                ? `Next attempt ${formatRelative(nextAttempt)}`
                : "")}
        </small>
        {blocked && lastError && (
          <small>Re-apply its settings or start it; any successful operation clears the block.</small>
        )}
      </span>
      {target.pendingOperation && (
        <Link className="row__meta" href={`/activity/${target.pendingOperation}`}>
          Operation
        </Link>
      )}
      <StateBadge state={state} label={badge} />
    </div>
  );
}
