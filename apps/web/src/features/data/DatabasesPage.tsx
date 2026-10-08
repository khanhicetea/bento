import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Database, Plus } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { EngineLogo } from "../../components/EngineLogo.tsx";
import { Cell, DomainError, DomainLoading, StateBadge } from "../../components/DomainState.tsx";
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
  const versions = engine === "mysql" ? catalog.data?.mysqlVersions : catalog.data?.postgresVersions;
  const selectedVersion = version || versions?.at(-1) || "";
  const create = useOperationMutation(() => api.services.create({ engine, version: selectedVersion }));
  return (
    <Cell title="Data services" icon={<Database />} className="flex flex-col">
      {q.isPending && <DomainLoading label="services" />}
      {q.error && <DomainError message={messageOf(q.error)} onRetry={() => void q.refetch()} />}
      {q.data && (
        <div className="grid gap-2 sm:grid-cols-2">
          {q.data.services.map((s) => (
            <article key={s.name} className="grid justify-items-start gap-2 rounded-[0.875rem] bg-background p-3.5">
              <span className="label flex items-center gap-2 text-xs">
                <EngineLogo engine={s.engine} className="size-4" />
                {s.name} <span className="font-mono tracking-normal">{s.version}</span>
              </span>
              <StateBadge
                state={s.initialized ? s.state : "starting"}
                title={s.message}
                label={s.initialized ? undefined : "Initializing"}
              />
              <code className="truncate text-xs text-muted-foreground" title={s.image}>
                {s.volume}
              </code>
              {s.message && s.state !== "healthy" && <p className="note note--bad">{s.message}</p>}
            </article>
          ))}
          {q.data.services.length === 0 && <p className="note">No services · add one below</p>}
        </div>
      )}
      <p className="note mt-3">Volumes are never removed.</p>
      <div className="mt-auto grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-end gap-2 border-t border-border pt-3">
        <label className="field">
          <span>Engine</span>
          <NativeSelect
            className="w-full"
            value={engine}
            onChange={(e) => {
              setEngine(e.target.value as T.Engine);
              setVersion("");
            }}
          >
            <option value="mysql">MySQL</option>
            <option value="postgres">PostgreSQL</option>
          </NativeSelect>
        </label>
        <label className="field">
          <span>Version</span>
          <NativeSelect
            className="w-full"
            value={selectedVersion}
            disabled={!versions?.length}
            onChange={(e) => setVersion(e.target.value)}
          >
            {!versions?.length && <option value="">{catalog.isPending ? "Loading…" : "None"}</option>}
            {(versions ?? []).map((v) => (
              <option key={v}>{v}</option>
            ))}
          </NativeSelect>
        </label>
        <Button
          variant="outline"
          onClick={() => create.mutate(undefined)}
          disabled={!selectedVersion || create.isPending}
        >
          <Plus /> Add
        </Button>
      </div>
      {create.error && <p className="note note--bad mt-2">{messageOf(create.error)}</p>}
    </Cell>
  );
}
