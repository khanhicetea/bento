import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Database, Plus } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { DomainError, DomainLoading, StateBadge } from "../../components/DomainState.tsx";
import { useCatalog, useOperationMutation } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/native-select";

/** Shared database services, shown on the System page below the host metrics. */
export function DataServices() {
  const q = useQuery({
    queryKey: keys.services,
    queryFn: ({ signal }) => api.services.list(signal),
    refetchInterval: 15_000,
  });
  const catalog = useCatalog();
  const [engine, setEngine] = useState<T.Engine>("mysql");
  const [version, setVersion] = useState("");
  const [adding, setAdding] = useState(false);
  const versions = engine === "mysql" ? catalog.data?.mysqlVersions : catalog.data?.postgresVersions;
  const selectedVersion = version || versions?.at(-1) || "";
  const create = useOperationMutation(() => api.services.create({ engine, version: selectedVersion }));
  return (
    <section id="data" aria-labelledby="data-services-title">
      <div className="mb-3 flex items-baseline justify-between gap-3 px-1">
        <h2 id="data-services-title" className="text-lg font-semibold">
          Data services
        </h2>
        <p className="text-sm text-muted-foreground max-sm:hidden">
          Shared databases on the private network. Volumes are never removed.
        </p>
      </div>
      {q.isPending && <DomainLoading label="services" />}
      {q.error && <DomainError message={messageOf(q.error)} onRetry={() => void q.refetch()} />}
      {q.data && (
        <div className="box">
          <div className="tiles">
            {q.data.services.map((s) => (
              <article key={s.name} className="cell tile">
                <div className="tile__top">
                  <span className="mono mono--lg">
                    <Database className="size-5" />
                  </span>
                  <div className="tile__name">
                    <strong>{s.name}</strong>
                    <small>
                      {s.engine === "mysql" ? "MySQL" : "PostgreSQL"} {s.version}
                    </small>
                  </div>
                  <StateBadge
                    state={s.initialized ? s.state : "starting"}
                    title={s.message}
                    label={s.initialized ? undefined : "Initializing"}
                  />
                </div>
                <dl className="facts facts--1">
                  <div>
                    <dt>Image</dt>
                    <dd>
                      <code>{s.image}</code>
                    </dd>
                  </div>
                  <div>
                    <dt>Volume</dt>
                    <dd>
                      <code>{s.volume}</code>
                    </dd>
                  </div>
                </dl>
                {s.message && s.state !== "healthy" && <p className="note note--bad">{s.message}</p>}
              </article>
            ))}
            {adding ? (
              <section className="cell cell--muted tile tile--add-form" aria-label="Add service">
                <div className="cell__title mb-0!">
                  <h2>Add service</h2>
                </div>
                <div className="grid-2">
                  <NativeSelect
                    className="w-full"
                    aria-label="Engine"
                    value={engine}
                    onChange={(e) => {
                      setEngine(e.target.value as T.Engine);
                      setVersion("");
                    }}
                  >
                    <option value="mysql">MySQL</option>
                    <option value="postgres">PostgreSQL</option>
                  </NativeSelect>
                  <NativeSelect
                    className="w-full"
                    aria-label="Version"
                    value={selectedVersion}
                    disabled={!versions?.length}
                    onChange={(e) => setVersion(e.target.value)}
                  >
                    {!versions?.length && <option value="">{catalog.isPending ? "Loading…" : "None"}</option>}
                    {(versions ?? []).map((v) => (
                      <option key={v}>{v}</option>
                    ))}
                  </NativeSelect>
                </div>
                <div className="actions">
                  <Button variant="ghost" onClick={() => setAdding(false)} disabled={create.isPending}>
                    Cancel
                  </Button>
                  <Button
                    onClick={() => create.mutate(undefined, { onSuccess: () => setAdding(false) })}
                    disabled={!selectedVersion || create.isPending}
                  >
                    <Plus /> Add
                  </Button>
                </div>
                {create.error && <p className="note note--bad">{messageOf(create.error)}</p>}
              </section>
            ) : (
              <button type="button" className="cell tile tile--add" onClick={() => setAdding(true)}>
                {q.data.services.length === 0 ? <Database aria-hidden="true" /> : <Plus aria-hidden="true" />}
                {q.data.services.length === 0 ? "No services yet · Add one" : "Add service"}
              </button>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
