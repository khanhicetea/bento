import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, KeyRound, Rocket } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { Cell, CopyableCode, DomainError, DomainLoading, Field, KeyValues } from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";
import { useOperationMutation } from "./useApplications.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function DeployPanel({ app }: { app: T.App }) {
  const query = useQuery({ queryKey: keys.apps.git(app.id), queryFn: ({ signal }) => api.apps.git(app.id, signal) });
  if (query.isPending) return <DomainLoading label="git source" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const source = query.data;
  return (
    <div className="box box--2">
      <SourceForm app={app} source={source} />
      {source.configured ? <DeployCell app={app} source={source} /> : <HowItWorks />}
      {source.configured && source.usesSsh && <DeployKeyCell app={app} source={source} />}
    </div>
  );
}

function SourceForm({ app, source }: { app: T.App; source: T.GitSource }) {
  const queryClient = useQueryClient();
  const [repoUrl, setRepoUrl] = useState(source.repoUrl);
  const [branch, setBranch] = useState(source.branch || "main");
  const [removeOpen, setRemoveOpen] = useState(false);
  const onSaved = (next: T.GitSource) => queryClient.setQueryData(keys.apps.git(app.id), next);
  const save = useMutation({
    mutationFn: () => api.apps.setGit(app.id, { repoUrl: repoUrl.trim(), branch: branch.trim() }),
    onSuccess: onSaved,
  });
  const remove = useMutation({ mutationFn: () => api.apps.removeGit(app.id), onSuccess: onSaved });
  const dirty = repoUrl.trim() !== source.repoUrl || branch.trim() !== source.branch;
  return (
    <Cell title="Repository">
      <form
        className="grid gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate();
        }}
      >
        <Field label="Repository URL" hint="SSH for private repos (deploy key). HTTPS only for public repos.">
          <Input
            value={repoUrl}
            placeholder="git@github.com:owner/repo.git"
            spellCheck={false}
            onChange={(event) => setRepoUrl(event.target.value)}
          />
        </Field>
        <Field label="Branch">
          <Input value={branch} spellCheck={false} onChange={(event) => setBranch(event.target.value)} />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={!repoUrl.trim() || !branch.trim() || !dirty || save.isPending}>
            <GitBranch /> {source.configured ? "Save" : "Connect repository"}
          </Button>
          {source.configured && (
            <Button type="button" variant="outline" onClick={() => setRemoveOpen(true)}>
              Disconnect
            </Button>
          )}
        </div>
        {save.error && <p className="note note--bad">{messageOf(save.error)}</p>}
      </form>
      <ConfirmDialog
        open={removeOpen}
        onOpenChange={setRemoveOpen}
        title="Disconnect repository?"
        description="The deploy key is destroyed; reconnecting generates a new key to register again. Code already in the app directory is kept."
        confirmLabel="Disconnect"
        destructive
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate(undefined, { onSuccess: () => setRemoveOpen(false) })}
      />
    </Cell>
  );
}

function DeployCell({ app, source }: { app: T.App; source: T.GitSource }) {
  const deploy = useOperationMutation(() => api.apps.deploy(app.id));
  return (
    <Cell title="Deploy">
      <KeyValues
        items={[
          ["Branch", <code>{source.branch}</code>],
          ["Deployed", source.deployedCommit ? <code>{source.deployedCommit.slice(0, 12)}</code> : "never"],
          ["When", formatRelative(source.deployedAt)],
        ]}
      />
      <p className="note my-3">
        Clones into an empty <code>app/</code> or resets tracked files to the branch head. Untracked files such as{" "}
        <code>.env</code> and <code>vendor/</code> are kept.{" "}
        {app.desiredRuntime === "running"
          ? app.runtime.kind === "php-fpm"
            ? "PHP-FPM then reloads gracefully; the container and scheduler keep running."
            : "The app process then restarts; the container and scheduler keep running."
          : "The app stays stopped."}
      </p>
      <Button disabled={deploy.isPending || !app.provisioned} onClick={() => deploy.mutate(undefined)}>
        <Rocket /> Deploy {source.branch}
      </Button>
      {deploy.error && <p className="note note--bad mt-2">{messageOf(deploy.error)}</p>}
    </Cell>
  );
}

function DeployKeyCell({ app, source }: { app: T.App; source: T.GitSource }) {
  const queryClient = useQueryClient();
  const [rotateOpen, setRotateOpen] = useState(false);
  const rotate = useMutation({
    mutationFn: () => api.apps.setGit(app.id, { repoUrl: source.repoUrl, branch: source.branch, rotateKey: true }),
    onSuccess: (next) => {
      queryClient.setQueryData(keys.apps.git(app.id), next);
      setRotateOpen(false);
    },
  });
  return (
    <Cell title="Deploy key" className="cell--wide">
      <p className="note mb-3">
        Add this public key to the repository as a read-only deploy key (GitHub: Settings → Deploy keys; GitLab:
        Settings → Repository → Deploy keys). The private key never leaves Bento.
      </p>
      <CopyableCode value={source.publicKey} />
      <KeyValues
        items={[
          ["Fingerprint", <code>{source.fingerprint}</code>],
          ["Created", formatRelative(source.keyCreatedAt)],
        ]}
      />
      <Button className="mt-3" variant="outline" onClick={() => setRotateOpen(true)}>
        <KeyRound /> Rotate key
      </Button>
      <ConfirmDialog
        open={rotateOpen}
        onOpenChange={setRotateOpen}
        title="Rotate deploy key?"
        description="A new key is generated and the current one stops working in Bento. Register the new public key with the repository before the next deploy."
        confirmLabel="Rotate"
        pending={rotate.isPending}
        error={rotate.error}
        onConfirm={() => rotate.mutate()}
      />
    </Cell>
  );
}

function HowItWorks() {
  return (
    <Cell title="How it works" className="cell--muted">
      <ol className="note grid list-decimal gap-1 pl-4">
        <li>Connect an SSH repository URL and branch.</li>
        <li>Bento generates an ed25519 deploy key for this app.</li>
        <li>Add the public key to the repository as a read-only deploy key.</li>
        <li>Deploy. The host key is pinned on first connect.</li>
      </ol>
    </Cell>
  );
}
