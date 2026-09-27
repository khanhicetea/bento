import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import {
  DomainError,
  DomainLoading,
  Field,
  Page,
  PageHeader,
  Panel,
  StateBadge,
} from "../../components/DomainState.tsx";
import { useCatalog, useOperationMutation } from "../applications/useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/native-select";

export function DatabasesPage() {
  const q = useQuery({
    queryKey: keys.services,
    queryFn: ({ signal }) => api.services.list(signal),
    refetchInterval: 15_000,
  });
  const catalog = useCatalog();
  const [engine, setEngine] = useState<T.Engine>("mysql");
  const [version, setVersion] = useState("8.4");
  const create = useOperationMutation(() => api.services.create({ engine, version }));
  const versions = engine === "mysql" ? catalog.data?.mysqlVersions : catalog.data?.postgresVersions;
  return (
    <Page>
      <PageHeader
        section="Data services"
        title="Data services"
        description="Shared MySQL, PostgreSQL, and Redis on the private data network. Services are add-only; volumes are never removed or replaced automatically."
      />
      {q.isPending && <DomainLoading label="services" />}
      {q.error && <DomainError message={messageOf(q.error)} onRetry={() => void q.refetch()} />}
      {q.data && (
        <Panel title="Services">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground uppercase">
              <tr>
                {["Name", "Engine", "Version", "State", "Volume"].map((h) => (
                  <th key={h} className="py-2">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {q.data.services.map((s) => (
                <tr key={s.name} className="border-t border-border">
                  <td className="py-2 font-medium">{s.name}</td>
                  <td>{s.engine}</td>
                  <td>{s.version}</td>
                  <td>
                    <StateBadge state={s.state} />{" "}
                    {!s.initialized && <span className="text-xs text-muted-foreground">initializing</span>}
                  </td>
                  <td className="text-xs">
                    <code>{s.volume}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}
      <Panel
        title="Add a managed version"
        description="Initialization creates a new volume once; established services refuse to start on a missing volume."
      >
        <div className="flex flex-wrap items-end gap-3">
          <Field label="Engine">
            <NativeSelect
              value={engine}
              onChange={(e) => {
                const next = e.target.value as T.Engine;
                setEngine(next);
                setVersion(next === "mysql" ? "8.4" : "17");
              }}
            >
              <option value="mysql">MySQL</option>
              <option value="postgres">PostgreSQL</option>
            </NativeSelect>
          </Field>
          <Field label="Version">
            <NativeSelect value={version} onChange={(e) => setVersion(e.target.value)}>
              {(versions ?? [version]).map((v) => (
                <option key={v}>{v}</option>
              ))}
            </NativeSelect>
          </Field>
          <Button onClick={() => create.mutate(undefined)} disabled={create.isPending}>
            Add service
          </Button>
        </div>
        {create.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(create.error)}
          </Alert>
        )}
      </Panel>
    </Page>
  );
}
