import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { DomainError, DomainLoading, Field, Page, PageHeader, StateBadge } from "../../components/DomainState.tsx";
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
  const [version, setVersion] = useState("");
  const versions = engine === "mysql" ? catalog.data?.mysqlVersions : catalog.data?.postgresVersions;
  const selectedVersion = version || versions?.at(-1) || "";
  const create = useOperationMutation(() => api.services.create({ engine, version: selectedVersion }));
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
        <section className="bento-service-section" aria-labelledby="services-title">
          <div className="bento-service-section__head">
            <h2 id="services-title">Managed services</h2>
            <span>
              {q.data.services.length} {q.data.services.length === 1 ? "service" : "services"}
            </span>
          </div>
          {q.data.services.length === 0 ? (
            <p className="bento-service-empty">No managed data services yet. Add a version below when you need one.</p>
          ) : (
            <div className="bento-service-grid">
              {q.data.services.map((s) => (
                <article key={s.name} className="bento-service-tile">
                  <div className="bento-service-tile__head">
                    <div>
                      <h3>{s.name}</h3>
                      <p>
                        {s.engine} · version {s.version}
                      </p>
                    </div>
                    <StateBadge state={s.state} />
                  </div>
                  <div className="bento-service-tile__foot">
                    <span>Persistent volume</span>
                    <code>{s.volume}</code>
                  </div>
                  {!s.initialized && <p className="bento-service-tile__pending">Initializing this service</p>}
                </article>
              ))}
            </div>
          )}
        </section>
      )}
      <section className="bento-service-add" aria-labelledby="service-add-title">
        <div>
          <h2 id="service-add-title">Add a managed version</h2>
          <p>Initialization creates a new volume once; established services refuse to start on a missing volume.</p>
        </div>
        <div className="bento-service-add__form">
          <Field label="Engine">
            <NativeSelect
              value={engine}
              onChange={(e) => {
                const next = e.target.value as T.Engine;
                setEngine(next);
                setVersion("");
              }}
            >
              <option value="mysql">MySQL</option>
              <option value="postgres">PostgreSQL</option>
            </NativeSelect>
          </Field>
          <Field label="Version">
            <NativeSelect
              value={selectedVersion}
              disabled={!versions?.length}
              onChange={(e) => setVersion(e.target.value)}
            >
              {!versions?.length && (
                <option value="">{catalog.isPending ? "Loading versions…" : "No versions available"}</option>
              )}
              {(versions ?? []).map((v) => (
                <option key={v}>{v}</option>
              ))}
            </NativeSelect>
          </Field>
          <Button onClick={() => create.mutate(undefined)} disabled={!selectedVersion || create.isPending}>
            Add service
          </Button>
        </div>
        {create.error && (
          <Alert variant="destructive" className="mt-3">
            {messageOf(create.error)}
          </Alert>
        )}
      </section>
    </Page>
  );
}
