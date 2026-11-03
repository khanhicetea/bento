import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { ArrowUpFromLine, Hammer, Layers, Undo2 } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { Cell, DomainError, DomainLoading, StateBadge } from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useOperationMutation } from "../applications/useApplications.ts";
import { useTrackOperation } from "../operations/OperationTracker.tsx";
import { Button } from "@/components/ui/button";

/** Manifest entries worth a glance; the rest (bases, package digest) sit in the tooltip. */
const headline = ["php", "node", "bun", "python", "composer", "pnpm"];

const toolchainName: Record<string, string> = { php: "PHP", node: "Node", bun: "Bun", python: "Python" };

/** Runtime images: one tile per runtime with its current build, rebuild, and rollout of apps on older builds. */
export function RuntimeImages() {
  const query = useQuery({
    queryKey: keys.runtimeImages,
    queryFn: ({ signal }) => api.system.runtimeImages(signal),
    refetchInterval: 15_000,
  });
  const track = useTrackOperation();
  const rebuildAll = useMutation({
    mutationFn: async (images: T.RuntimeImage[]) => {
      for (const image of images) track(await api.system.rebuildRuntime(image.key));
    },
  });
  const images = query.data?.images ?? [];
  return (
    <div className="box">
      <Cell
        title="Runtimes"
        icon={<Layers />}
        className="[container-type:inline-size]"
        action={
          images.length > 0 && (
            <Button
              size="xs"
              variant="outline"
              disabled={rebuildAll.isPending}
              onClick={() => rebuildAll.mutate(images)}
            >
              <Hammer /> {rebuildAll.isPending ? "Rebuilding" : "Rebuild all"}
            </Button>
          )
        }
      >
        {query.isPending && <DomainLoading label="runtimes" />}
        {query.error && <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />}
        {query.data && images.length === 0 && <p className="note">No runtime in use</p>}
        {images.length > 0 && (
          <div className="tiles tiles--3">
            {images.map((image) => (
              <RuntimeTile key={image.key} image={image} />
            ))}
          </div>
        )}
        {rebuildAll.error && <p className="note note--bad">{messageOf(rebuildAll.error)}</p>}
      </Cell>
    </div>
  );
}

function runtimeState(image: T.RuntimeImage, behind: number) {
  if (!image.current) return { state: "absent", label: "Not built" };
  if (image.apps.some((a) => a.rolling)) return { state: "running", label: "Rolling out" };
  if (behind > 0) return { state: "pending", label: "Update" };
  return { state: "healthy", label: "Current" };
}

function RuntimeTile({ image }: { image: T.RuntimeImage }) {
  const [confirmAll, setConfirmAll] = useState(false);
  const rebuild = useOperationMutation(() => api.system.rebuildRuntime(image.key));
  const rollout = useOperationMutation((body: T.RolloutRequest) => api.system.rolloutRuntime(image.key, body));
  const behind = image.apps.filter((a) => a.behind);
  const running = behind.filter((a) => a.desiredRuntime === "running");
  const { state, label } = runtimeState(image, behind.length);
  const manifest = image.current?.manifest ?? {};
  const versions = headline.filter((k) => manifest[k]).map((k) => `${k} ${manifest[k]}`);
  const details = Object.entries(manifest)
    .map(([k, v]) => `${k} ${v}`)
    .join("\n");
  return (
    <article className="grid content-start gap-2 rounded-[0.875rem] bg-background p-3.5">
      <div className="flex w-full items-start justify-between gap-2">
        <span className="label flex min-w-0 items-center gap-2 text-xs">
          <span className="truncate">{toolchainName[image.toolchain] ?? image.toolchain}</span>
          <span className="font-mono tracking-normal">{image.version}</span>
        </span>
        <StateBadge state={state} label={label} />
      </div>
      {image.current ? (
        <>
          <code className="truncate text-xs text-muted-foreground" title={details}>
            {image.current.tag.replace(/^bento-runtime\//, "")}
          </code>
          <p className="text-xs text-muted-foreground" title={details}>
            {versions.join(" · ") || "—"} · built {formatRelative(image.current.builtAt)}
          </p>
        </>
      ) : (
        <p className="text-xs text-muted-foreground">Builds when an app needs it</p>
      )}
      {image.apps.length > 0 && (
        <div className="rows rows--lined w-full">
          {image.apps.map((app) => (
            <AppRow key={app.id} app={app} pending={rollout.isPending} onRollout={(body) => rollout.mutate(body)} />
          ))}
        </div>
      )}
      {behind.length > 0 && (
        <p className="note note--caution">
          ▲ {behind.length} {behind.length === 1 ? "app" : "apps"} on an older build
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button size="xs" variant="outline" disabled={rebuild.isPending} onClick={() => rebuild.mutate(undefined)}>
          <Hammer /> {rebuild.isPending ? "Rebuilding" : "Rebuild"}
        </Button>
        {behind.length > 0 && (
          <Button size="xs" disabled={rollout.isPending} onClick={() => setConfirmAll(true)}>
            <ArrowUpFromLine /> {rollout.isPending ? "Rolling out" : `Roll out ${behind.length}`}
          </Button>
        )}
      </div>
      {rebuild.error && <p className="note note--bad">{messageOf(rebuild.error)}</p>}
      {rollout.error && <p className="note note--bad">{messageOf(rollout.error)}</p>}
      <ConfirmDialog
        open={confirmAll}
        onOpenChange={setConfirmAll}
        title={`Roll out ${image.key}`}
        description={
          running.length > 0
            ? `Replaces ${running.length} running ${running.length === 1 ? "app" : "apps"} one at a time on ${image.current?.tag ?? "the current build"}. Each is briefly unavailable while it restarts. The first failed replacement stops the rollout and puts the apps not yet replaced back on their previous build.`
            : "Stopped apps start on the current build next time."
        }
        confirmLabel="Roll out"
        pending={rollout.isPending}
        error={rollout.error ?? undefined}
        onConfirm={() => rollout.mutate({}, { onSuccess: () => setConfirmAll(false) })}
      />
    </article>
  );
}

function AppRow({
  app,
  pending,
  onRollout,
}: {
  app: T.RuntimeImageApp;
  pending: boolean;
  onRollout: (body: T.RolloutRequest) => void;
}) {
  const state = app.rolling
    ? { state: "running", label: "Rolling" }
    : app.behind
      ? { state: "pending", label: "Behind" }
      : app.buildId === 0
        ? { state: "absent", label: "Not built" }
        : { state: "healthy", label: "Current" };
  return (
    <div className="row">
      <span className="row__main">
        <strong>{app.slug}</strong>
        {app.changes.length > 0 && <small title={app.changes.join("\n")}>{app.changes.join(" · ")}</small>}
      </span>
      <StateBadge state={state.state} label={state.label} iconOnly />
      {app.behind && !app.rolling && (
        <Button
          size="xs"
          variant="ghost"
          disabled={pending}
          aria-label={`Roll out ${app.slug}`}
          onClick={() => onRollout({ appIds: [app.id] })}
        >
          <ArrowUpFromLine /> Roll out
        </Button>
      )}
      {!app.behind && !app.rolling && app.rollbackBuildId > 0 && (
        <Button
          size="xs"
          variant="ghost"
          disabled={pending}
          aria-label={`Roll back ${app.slug}`}
          onClick={() => onRollout({ appIds: [app.id], buildId: app.rollbackBuildId })}
        >
          <Undo2 /> Roll back
        </Button>
      )}
    </div>
  );
}
