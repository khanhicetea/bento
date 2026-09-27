import { useState } from "react";
import { Search } from "lucide-react";
import { messageOf, type T } from "../../api/client.ts";
import { DomainError, DomainLoading, EmptyPanel, Page, PageHeader, StateBadge } from "../../components/DomainState.tsx";
import { ApplicationDetail } from "./ApplicationDetail.tsx";
import { ApplicationEditor } from "./ApplicationEditor.tsx";
import { useAppAction, useApplicationList, type AppAction } from "./useApplications.ts";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function ApplicationsPage() {
  const list = useApplicationList();
  const action = useAppAction();
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const apps = (list.data?.apps ?? []).filter((a) =>
    `${a.slug} ${a.primaryDomain}`.includes(query.trim().toLowerCase()),
  );

  function run(app: T.AppSummary, verb: AppAction) {
    action.mutate({ id: app.id, action: verb });
  }

  return (
    <Page>
      <PageHeader
        section="Applications"
        title="Applications"
        description="One persistent container per app. Desired intent is shown beside Docker-observed state."
        actions={<Button onClick={() => setCreating(true)}>+ New application</Button>}
      />
      {action.error && (
        <Alert variant="destructive" className="mb-4">
          {messageOf(action.error)}
        </Alert>
      )}
      {list.isPending && <DomainLoading label="applications" />}
      {list.error && <DomainError message={messageOf(list.error)} onRetry={() => void list.refetch()} />}
      {list.data && (
        <div className="rounded-xl border border-border bg-card shadow-sm">
          <div className="flex items-center gap-2 border-b border-border p-3">
            <Search className="size-4 opacity-50" />
            <Input
              className="max-w-sm"
              placeholder="Filter by slug or domain"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {apps.length === 0 ? (
            <EmptyPanel>No applications yet.</EmptyPanel>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead className="text-left text-xs text-muted-foreground uppercase">
                  <tr>
                    {["App", "Runtime", "Desired", "Observed", "Ingress", "Domain", ""].map((h) => (
                      <th key={h} className="px-4 py-2 font-medium">
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {apps.map((app) => (
                    <tr key={app.id} className="border-t border-border">
                      <td className="px-4 py-3">
                        <button className="font-semibold hover:underline" onClick={() => setSelected(app.id)}>
                          {app.slug}
                        </button>
                        <div className="text-xs text-muted-foreground">
                          uid {app.uid} · {app.id}
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        {app.toolchain} {app.version}
                      </td>
                      <td className="px-4 py-3">{app.desiredRuntime}</td>
                      <td className="px-4 py-3">
                        <StateBadge state={app.observed.state} title={app.observed.message} />
                        {app.observed.message && (
                          <div className="mt-1 max-w-64 truncate text-xs text-muted-foreground">
                            {app.observed.message}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3">
                        <Badge variant="outline">{app.ingress}</Badge>{" "}
                        {app.ingress === "managed" && (
                          <Badge variant={app.publication === "published" ? "default" : "outline"}>
                            {app.publication}
                          </Badge>
                        )}
                      </td>
                      <td className="px-4 py-3 text-xs">{app.primaryDomain || "—"}</td>
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap justify-end gap-1.5">
                          {app.desiredRuntime === "stopped" ? (
                            <Button size="xs" onClick={() => run(app, "start")}>
                              Start
                            </Button>
                          ) : (
                            <>
                              <Button size="xs" variant="outline" onClick={() => run(app, "restart")}>
                                Restart
                              </Button>
                              <Button size="xs" variant="outline" onClick={() => run(app, "stop")}>
                                Stop
                              </Button>
                            </>
                          )}
                          {app.ingress === "managed" && app.desiredRuntime === "running" && (
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => run(app, app.publication === "published" ? "unpublish" : "publish")}
                            >
                              {app.publication === "published" ? "Unpublish" : "Publish"}
                            </Button>
                          )}
                          <Button size="xs" variant="ghost" onClick={() => setSelected(app.id)}>
                            Details
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
      {creating && <ApplicationEditor app={null} onClose={() => setCreating(false)} />}
      {selected && <ApplicationDetail id={selected} onClose={() => setSelected(null)} />}
    </Page>
  );
}
