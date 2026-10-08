import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Play } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Field, KeyValues } from "../../components/DomainState.tsx";
import { formatBytes } from "../../lib/format.ts";
import { isTerminal, useTrackOperation } from "../operations/OperationTracker.tsx";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";

type Step = "repository" | "options" | "preview" | "confirm" | "result";

/** Where the snapshot comes from: an app of this stack, or a repository of another stack. */
type CloneSource = { kind: "app"; app: T.App; snapshot: T.ResticSnapshot } | { kind: "repository" };

/** Polls an accepted operation until it reaches a terminal state. */
function useOperationState(id: string) {
  const queryClient = useQueryClient();
  return useQuery({
    queryKey: keys.operations.detail(id),
    queryFn: async ({ signal }) => {
      const op = await api.operations.get(id, signal);
      if (isTerminal(op.state)) void queryClient.invalidateQueries({ queryKey: keys.apps.list() });
      return op;
    },
    enabled: id !== "",
    refetchInterval: (query) => (isTerminal(query.state.data?.state) ? false : 1000),
  });
}

function OperationProgress({ op, label }: { op: T.Operation | undefined; label: string }) {
  const last = op?.events?.at(-1)?.message;
  return (
    <p className="note flex items-center gap-2">
      <Spinner /> {label}
      {op?.phase ? ` (${op.phase})` : ""}
      {last ? ` · ${last}` : ""}
    </p>
  );
}

const yesNo = (value: boolean) => (value ? "yes" : "no");

/**
 * Restore a snapshot into a new app on this stack: options, preview (read-only
 * inspect), confirmation, then the checklist of the stopped clone.
 */
export function CloneFromBackupDialog({
  app,
  snapshot,
  onClose,
}: {
  app: T.App;
  snapshot: T.ResticSnapshot | null;
  onClose: () => void;
}) {
  return (
    <Dialog open={snapshot !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        {snapshot && <CloneSteps key={snapshot.id} source={{ kind: "app", app, snapshot }} onClose={onClose} />}
      </DialogContent>
    </Dialog>
  );
}

/**
 * Apps → New → From app backup: the same dialog with a repository and key step
 * first, for a backup made on another stack.
 */
export function RestoreFromBackupDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        {open && <CloneSteps source={{ kind: "repository" }} onClose={onClose} />}
      </DialogContent>
    </Dialog>
  );
}

function CloneSteps({ source, onClose }: { source: CloneSource; onClose: () => void }) {
  const track = useTrackOperation();
  const remote = source.kind === "repository";
  const [step, setStep] = useState<Step>(remote ? "repository" : "options");
  const [repository, setRepository] = useState("");
  const [key, setKey] = useState("");
  const [chosen, setChosen] = useState("");
  const [listId, setListId] = useState("");
  const [slug, setSlug] = useState(source.kind === "app" ? `${source.app.slug}-copy` : "");
  const [keepUsername, setKeepUsername] = useState(false);
  const [backupAfter, setBackupAfter] = useState<"none" | "new-repo" | "same-repo">("none");
  const [typed, setTyped] = useState("");
  const [inspectId, setInspectId] = useState("");
  const [cloneId, setCloneId] = useState("");
  const listOp = useOperationState(listId).data;
  const listing =
    listOp?.state === "succeeded" ? (listOp.result as unknown as T.ResticClonePreview | undefined) : undefined;
  const snapshotId = source.kind === "app" ? source.snapshot.id : chosen || listing?.snapshots[0]?.id || "";
  const read = useMutation({
    mutationFn: () =>
      api.apps.restoreFromBackup.inspect({
        repository: repository.trim(),
        key,
        snapshot: "",
        slug: "",
        keepUsername: false,
      }),
    onSuccess: (accepted) => setListId(accepted.operation.id),
  });
  const inspect = useMutation({
    mutationFn: () =>
      source.kind === "app"
        ? api.apps.restic.inspect(source.app.id, { snapshot: snapshotId, slug: slug.trim(), keepUsername })
        : api.apps.restoreFromBackup.inspect({
            repository: repository.trim(),
            key,
            snapshot: snapshotId,
            slug: slug.trim(),
            keepUsername,
          }),
    onSuccess: (accepted) => {
      setInspectId(accepted.operation.id);
      setStep("preview");
    },
  });
  const clone = useMutation({
    mutationFn: () =>
      source.kind === "app"
        ? api.apps.restic.clone(source.app.id, {
            snapshot: snapshotId,
            slug: slug.trim(),
            keepUsername,
            backupAfter: backupAfter === "new-repo" ? "new-repo" : "none",
            confirm: typed,
          })
        : api.apps.restoreFromBackup.clone({
            repository: repository.trim(),
            key,
            snapshot: snapshotId,
            slug: slug.trim(),
            keepUsername,
            backupAfter,
            confirm: typed,
          }),
    onSuccess: (accepted) => {
      track(accepted);
      setKey("");
      setCloneId(accepted.operation.id);
      setStep("result");
    },
  });
  const inspectOp = useOperationState(inspectId).data;
  const cloneOp = useOperationState(cloneId).data;
  const phrase = `clone ${slug.trim()}`;

  if (step === "repository") {
    const listFailed = listOp && isTerminal(listOp.state) && listOp.state !== "succeeded";
    const reading = listId !== "" && !listing && !listFailed;
    return (
      <>
        <DialogHeader>
          <DialogTitle>Restore from an app backup</DialogTitle>
          <DialogDescription>
            Open the repository of an app backup made on another stack. Use a key from <b>Add key</b> on the source app.
            The key is only used for this restore and is deleted afterwards, unless you keep backing up to the
            repository.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <Field
            label="Repository"
            hint="An rclone remote path from this stack's rclone config, for example s3:bucket/shop."
          >
            <Input
              value={repository}
              spellCheck={false}
              autoFocus
              onChange={(event) => {
                setRepository(event.target.value);
                setListId("");
              }}
            />
          </Field>
          <Field label="Repository key">
            <Input
              type="password"
              value={key}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => {
                setKey(event.target.value);
                setListId("");
              }}
            />
          </Field>
          {reading && <OperationProgress op={listOp} label="Reading the repository" />}
          {listFailed && (
            <Alert variant="destructive">
              {listOp.errorMessage}
              {listOp.guidance ? `\n${listOp.guidance}` : ""}
            </Alert>
          )}
          {listing && (
            <Field label={`Snapshot of ${listing.sourceSlug}`}>
              <NativeSelect value={snapshotId} onChange={(event) => setChosen(event.target.value)}>
                {listing.snapshots.map((snap) => (
                  <option key={snap.id} value={snap.id}>
                    {snap.shortId} · {snap.time}
                    {snap.tags.includes("secrets=1") ? " · secrets" : ""}
                  </option>
                ))}
              </NativeSelect>
            </Field>
          )}
        </div>
        {read.error && <Alert variant="destructive">{messageOf(read.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          {listing ? (
            <Button
              onClick={() => {
                if (slug.trim() === "") setSlug(`${listing.sourceSlug}-copy`);
                setStep("options");
              }}
            >
              Continue
            </Button>
          ) : (
            <Button
              disabled={repository.trim() === "" || key.trim() === "" || read.isPending || reading}
              onClick={() => read.mutate()}
            >
              Read repository
            </Button>
          )}
        </DialogFooter>
      </>
    );
  }

  if (step === "options") {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Restore into a new app · 1 of 3</DialogTitle>
          <DialogDescription>
            {source.kind === "app" ? (
              <>
                Snapshot <code>{source.snapshot.shortId}</code> becomes a new app next to <code>{source.app.slug}</code>
                . The original is never changed.
              </>
            ) : (
              <>
                Snapshot <code>{snapshotId.slice(0, 8)}</code> of <code>{listing?.sourceSlug}</code> becomes a new app
                on this stack. The backup is never changed.
              </>
            )}{" "}
            The new app stays stopped until you start it.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <Field label="New app slug" hint="Database names become <slug>_<name>.">
            <Input value={slug} spellCheck={false} autoFocus onChange={(event) => setSlug(event.target.value)} />
          </Field>
          <label className="check">
            <Checkbox checked={keepUsername} onCheckedChange={(checked) => setKeepUsername(checked === true)} />
            <span>
              Keep the database username when it is free
              <small className="block text-muted-foreground">
                On this stack the original still uses it, so the clone normally gets a new user.
              </small>
            </span>
          </label>
          {source.kind === "app" ? (
            <label className="check">
              <Checkbox
                checked={backupAfter === "new-repo"}
                onCheckedChange={(checked) => setBackupAfter(checked === true ? "new-repo" : "none")}
              />
              <span>
                Prepare app backup for the new app
                <small className="block text-muted-foreground">
                  Saves backup settings that point at a new repository; you initialize it later.
                </small>
              </span>
            </label>
          ) : (
            <Field
              label="App backup of the new app"
              hint="Keeping the repository stores the key you entered for the new app; its schedule stays off."
            >
              <NativeSelect
                value={backupAfter}
                onChange={(event) => setBackupAfter(event.target.value as typeof backupAfter)}
              >
                <option value="none">Set it up later</option>
                <option value="new-repo">Prepare a new repository for it</option>
                <option value="same-repo">Keep backing up to this repository</option>
              </NativeSelect>
            </Field>
          )}
        </div>
        {inspect.error && <Alert variant="destructive">{messageOf(inspect.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={() => (remote ? setStep("repository") : onClose())}>
            {remote ? "Back" : "Cancel"}
          </Button>
          <Button disabled={slug.trim().length < 3 || inspect.isPending} onClick={() => inspect.mutate()}>
            Preview
          </Button>
        </DialogFooter>
      </>
    );
  }

  if (step === "preview") {
    const failed = inspectOp && isTerminal(inspectOp.state) && inspectOp.state !== "succeeded";
    const preview =
      inspectOp?.state === "succeeded" ? (inspectOp.result as unknown as T.ResticClonePreview | undefined) : undefined;
    return (
      <>
        <DialogHeader>
          <DialogTitle>Restore into a new app · 2 of 3</DialogTitle>
          <DialogDescription>
            What the new app <code>{slug.trim()}</code> will be.
          </DialogDescription>
        </DialogHeader>
        {!inspectOp || (!preview && !failed) ? <OperationProgress op={inspectOp} label="Reading the snapshot" /> : null}
        {failed && (
          <Alert variant="destructive">
            {inspectOp.errorMessage}
            {inspectOp.guidance ? `\n${inspectOp.guidance}` : ""}
          </Alert>
        )}
        {preview && <PreviewBody preview={preview} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => setStep("options")}>
            Back
          </Button>
          <Button disabled={!preview || preview.blockers.length > 0} onClick={() => setStep("confirm")}>
            Continue
          </Button>
        </DialogFooter>
      </>
    );
  }

  if (step === "confirm") {
    return (
      <>
        <DialogHeader>
          <DialogTitle>Restore into a new app · 3 of 3</DialogTitle>
          <DialogDescription>
            This creates app <code>{slug.trim()}</code> with new databases and a copy of the files. Nothing starts.
          </DialogDescription>
        </DialogHeader>
        <label className="grid gap-2 text-sm font-medium">
          Type <code>{phrase}</code> to confirm
          <Input value={typed} autoComplete="off" autoFocus onChange={(event) => setTyped(event.target.value)} />
        </label>
        {clone.error && <Alert variant="destructive">{messageOf(clone.error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={() => setStep("preview")}>
            Back
          </Button>
          <Button disabled={typed !== phrase || clone.isPending} onClick={() => clone.mutate()}>
            Create app
          </Button>
        </DialogFooter>
      </>
    );
  }

  const done = cloneOp?.state === "succeeded";
  const failed = cloneOp && isTerminal(cloneOp.state) && !done;
  const result = done ? (cloneOp.result as unknown as T.ResticCloneResult | undefined) : undefined;
  return (
    <>
      <DialogHeader>
        <DialogTitle>{result ? `${result.slug} is ready (stopped)` : "Restoring into a new app"}</DialogTitle>
        <DialogDescription>
          {result ? "Check these before you start it." : "Downloading the snapshot and building the new app."}
        </DialogDescription>
      </DialogHeader>
      {!cloneOp || (!result && !failed) ? <OperationProgress op={cloneOp} label="Restoring" /> : null}
      {failed && (
        <Alert variant="destructive">
          {cloneOp.errorMessage}
          {cloneOp.guidance ? `\n${cloneOp.guidance}` : ""}
        </Alert>
      )}
      {result && <Checklist result={result} />}
      <DialogFooter>
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
        {result && <ResultActions result={result} onClose={onClose} />}
      </DialogFooter>
    </>
  );
}

function ResultActions({ result, onClose }: { result: T.ResticCloneResult; onClose: () => void }) {
  const track = useTrackOperation();
  const start = useMutation({
    mutationFn: () => api.apps.action(result.appId, "start"),
    onSuccess: (accepted) => {
      track(accepted);
      onClose();
    },
  });
  return (
    <>
      {start.error && <p className="note note--bad">{messageOf(start.error)}</p>}
      <Button variant="outline" asChild>
        <Link href={`/apps/${encodeURIComponent(result.slug)}`} onClick={onClose}>
          Open app
        </Link>
      </Button>
      <Button disabled={start.isPending} onClick={() => start.mutate()}>
        <Play /> Start
      </Button>
    </>
  );
}

function Checklist({ result }: { result: T.ResticCloneResult }) {
  return (
    <ul className="grid list-disc gap-1.5 pl-5 text-sm">
      {result.checklist.map((line) => (
        <li key={line} className="break-words">
          {line}
        </li>
      ))}
    </ul>
  );
}

function PreviewBody({ preview }: { preview: T.ResticClonePreview }) {
  return (
    <div className="grid gap-3 text-sm">
      {preview.blockers.length > 0 && (
        <Alert variant="destructive">
          <ul className="list-disc pl-4">
            {preview.blockers.map((blocker) => (
              <li key={blocker}>{blocker}</li>
            ))}
          </ul>
        </Alert>
      )}
      <KeyValues
        items={[
          ["Source", `${preview.sourceSlug} @ ${preview.snapshotTime} (format ${preview.formatVersion})`],
          ["Size", preview.sizeBytes > 0 ? formatBytes(preview.sizeBytes) : "unknown"],
          ["Runtime", `${preview.runtimeKind} ${preview.runtimeVersion}`.trim()],
          ["Resources", `${preview.resources.memoryMb} MB · ${preview.resources.cpuMillis / 1000} CPU`],
          ["Home inside the container", <code>{preview.homePath}</code>],
          ["Secrets in the snapshot", yesNo(preview.secrets)],
          ["Scheduler jobs restored", yesNo(preview.minicron)],
          ["Git source and new deploy key", yesNo(preview.git)],
        ]}
      />
      {preview.databases.length > 0 && (
        <div>
          <h4 className="mb-1 font-medium">Databases</h4>
          <ul className="grid gap-1">
            {preview.databases.map((db) => (
              <li key={`${db.service}/${db.source}`}>
                <code>
                  {db.source} ({db.service}, {db.version})
                </code>{" "}
                → <code>{db.target}</code>
                <span className="block text-muted-foreground">
                  user {db.usernameKept ? "kept" : `new (${db.username})`} · password {db.passwordKept ? "kept" : "new"}
                  {db.usernameNote ? ` · ${db.usernameNote}` : ""}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {preview.sqlite.length > 0 && (
        <div>
          <h4 className="mb-1 font-medium">SQLite files</h4>
          <ul className="grid gap-1">
            {preview.sqlite.map((file) => (
              <li key={file.target}>
                <code>{file.source}</code> → <code>{file.target}</code>
              </li>
            ))}
          </ul>
        </div>
      )}
      {preview.emptyEnv.length > 0 && (
        <p>
          Set after restore (not in the backup): <code>{preview.emptyEnv.join(", ")}</code>
        </p>
      )}
      {preview.domains.length > 0 && (
        <p>
          Domains are not moved. Attach them after checking the clone:{" "}
          {preview.domains.map((domain) => `${domain.name}${domain.inUse ? " (in use here)" : ""}`).join(", ")}
        </p>
      )}
      {preview.notes.map((note) => (
        <p key={note} className="note">
          {note}
        </p>
      ))}
    </div>
  );
}
