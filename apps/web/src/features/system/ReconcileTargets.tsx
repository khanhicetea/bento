import { useQuery } from "@tanstack/react-query";
import { Link } from "wouter";
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
    <div className="box">
      <Cell title="Reconciliation">
        {query.error ? (
          <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
        ) : query.isPending ? (
          <p className="note">Loading…</p>
        ) : targets.length === 0 ? (
          <p className="note">Everything converged</p>
        ) : (
          <div className="rows rows--lined">
            {targets.map((target) => (
              <TargetRow key={target.id} target={target} label={label(target.id)} />
            ))}
          </div>
        )}
      </Cell>
    </div>
  );
}

function TargetRow({ target, label }: { target: T.ReconcileTarget; label: string }) {
  const { failures, blocked, nextAttempt, lastError } = target.reconcile;
  const state = blocked ? "blocked" : target.pendingOperation ? "queued" : "failed";
  const badge = blocked ? "Blocked" : target.pendingOperation ? "Repairing" : "Retrying";
  return (
    <div className="row">
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
