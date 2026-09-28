import { useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Box, HardDrive, Network, Trash2 } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { Cell, DomainError, DomainLoading } from "../../components/DomainState.tsx";
import { formatBytes, formatRelative } from "../../lib/format.ts";
import { useOperationMutation } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";

export type DockerKind = "images" | "volumes" | "networks";

export function DockerInventory({ kind }: { kind: DockerKind }) {
  const query = useQuery({ queryKey: keys.dockerInventory, queryFn: ({ signal }) => api.system.docker(signal) });
  const [target, setTarget] = useState<T.DockerImage | null>(null);
  const prune = useOperationMutation((confirm: string) => api.system.pruneImage(target?.id ?? "", confirm));
  if (query.isPending) return <DomainLoading label="Docker resources" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const inv = query.data;
  const count = { images: inv.images.length, volumes: inv.volumes.length, networks: inv.networks.length }[kind];
  return (
    <>
      <Summary inventory={inv} />
      <div className="box">
        <Cell>
          {count === 0 ? (
            <p className="note">Nothing to show</p>
          ) : (
            <div className="rows rows--lined">
              {kind === "images" &&
                inv.images.map((image) => <ImageRow key={image.id} image={image} onPrune={() => setTarget(image)} />)}
              {kind === "volumes" && inv.volumes.map((volume) => <VolumeRow key={volume.name} volume={volume} />)}
              {kind === "networks" && inv.networks.map((network) => <NetworkRow key={network.id} network={network} />)}
            </div>
          )}
        </Cell>
      </div>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={`Prune ${target?.tags[0] ?? shortId(target?.id ?? "")}?`}
        description="Removes this unused Bento-built image. Bento rebuilds it if an app needs it again."
        phrase="delete"
        destructive
        confirmLabel="Prune image"
        pending={prune.isPending}
        error={prune.error}
        onConfirm={(typed) => prune.mutate(typed, { onSuccess: () => setTarget(null) })}
      />
    </>
  );
}

function shortId(id: string) {
  return id.replace(/^sha256:/, "").slice(0, 12);
}

function Summary({ inventory }: { inventory: T.DockerInventory }) {
  const ours = inventory.images.filter((image) => image.ownership === "stack");
  const size = (items: T.DockerImage[]) => items.reduce((sum, image) => sum + image.sizeBytes, 0);
  const count = (items: Array<{ ownership: T.DockerOwnership }>) =>
    items.filter((item) => item.ownership === "stack").length;
  return (
    <div className="box box--3">
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{formatBytes(size(ours))}</strong>
          <span>
            {ours.length} images · {formatBytes(size(ours.filter((image) => image.prunable)))} reclaimable
          </span>
        </div>
      </Cell>
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{count(inventory.volumes)}</strong>
          <span>Volumes</span>
        </div>
      </Cell>
      <Cell>
        <div className="metric">
          <strong className="text-xl!">{count(inventory.networks)}</strong>
          <span>Networks</span>
        </div>
      </Cell>
    </div>
  );
}

function ResourceRow({
  ownership,
  icon,
  title,
  detail,
  usedBy,
  meta,
  action,
}: {
  ownership: T.DockerOwnership;
  icon: ReactNode;
  title: ReactNode;
  detail: ReactNode;
  usedBy: string[];
  meta?: ReactNode;
  action?: ReactNode;
}) {
  const ours = ownership === "stack";
  return (
    <div className={`row ${ours ? "" : "row--muted"}`}>
      {icon}
      <span className="row__main">
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
      <span className="tags max-sm:hidden">
        {!ours ? (
          <span className="tag">Other stack</span>
        ) : usedBy.length > 0 ? (
          <span className="tag" title={usedBy.join(", ")}>
            Used by {usedBy.length > 2 ? `${usedBy.slice(0, 2).join(", ")} +${usedBy.length - 2}` : usedBy.join(", ")}
          </span>
        ) : (
          <span className="tag">Unused</span>
        )}
      </span>
      {meta && <span className="row__meta max-sm:hidden">{meta}</span>}
      {action}
    </div>
  );
}

function ImageRow({ image, onPrune }: { image: T.DockerImage; onPrune: () => void }) {
  return (
    <ResourceRow
      ownership={image.ownership}
      icon={<Box />}
      title={
        image.tags.length > 0 ? image.tags.join(", ") : <span className="text-muted-foreground">&lt;none&gt;</span>
      }
      detail={`${shortId(image.id)} · ${image.built ? "built" : "pulled"} · ${formatBytes(image.sizeBytes)}`}
      usedBy={image.usedBy}
      meta={<span title={image.createdAt}>{formatRelative(image.createdAt)}</span>}
      action={
        image.prunable && (
          <Button size="sm" variant="ghost" className="text-destructive" onClick={onPrune}>
            <Trash2 /> Prune
          </Button>
        )
      }
    />
  );
}

function VolumeRow({ volume }: { volume: T.DockerVolume }) {
  return (
    <ResourceRow
      ownership={volume.ownership}
      icon={<HardDrive />}
      title={volume.name}
      detail={volume.service ? `Service ${volume.service}` : "Volume"}
      usedBy={volume.usedBy}
    />
  );
}

function NetworkRow({ network }: { network: T.DockerNetwork }) {
  const detail = [network.driver, network.internal ? "internal" : "", ...network.subnets].filter(Boolean).join(" · ");
  return (
    <ResourceRow
      ownership={network.ownership}
      icon={<Network />}
      title={network.name}
      detail={detail}
      usedBy={network.usedBy}
    />
  );
}
