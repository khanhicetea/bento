import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { GitBranch, KeyRound, Rocket, Webhook } from "lucide-react";
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
      {source.configured && <WebhookCell app={app} source={source} />}
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
        <code>.env</code> and <code>vendor/</code> are kept. An executable <code>~/deploy.sh</code> then runs as the app
        from <code>app/</code>; if it fails, the app is not reloaded.{" "}
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

function WebhookCell({ app, source }: { app: T.App; source: T.GitSource }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: keys.apps.webhook(app.id),
    queryFn: ({ signal }) => api.apps.webhook(app.id, signal),
  });
  // The secret is returned once by enable/rotate and never refetchable.
  const [secret, setSecret] = useState("");
  const [rotateOpen, setRotateOpen] = useState(false);
  const [disableOpen, setDisableOpen] = useState(false);
  const enable = useMutation({
    mutationFn: () => api.apps.enableWebhook(app.id),
    onSuccess: ({ secret: next, ...hook }) => {
      queryClient.setQueryData(keys.apps.webhook(app.id), hook);
      setSecret(next);
      setRotateOpen(false);
    },
  });
  const disable = useMutation({
    mutationFn: () => api.apps.disableWebhook(app.id),
    onSuccess: (hook) => {
      queryClient.setQueryData(keys.apps.webhook(app.id), hook);
      setSecret("");
      setDisableOpen(false);
    },
  });
  if (query.isPending) return <DomainLoading label="webhook" />;
  if (query.error) return <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />;
  const hook = query.data;
  if (!hook.enabled) {
    return (
      <Cell title="Push to deploy" className="cell--wide">
        <p className="note mb-3">
          A webhook URL that deploys <code>{source.branch}</code> when your git host reports a push to it. Works with
          GitHub, GitLab, Gitea, Forgejo, Bitbucket, or <code>curl</code> from CI. Requests are verified with a secret.
        </p>
        <Button variant="outline" disabled={enable.isPending} onClick={() => enable.mutate()}>
          <Webhook /> Enable webhook
        </Button>
        {enable.error && <p className="note note--bad mt-2">{messageOf(enable.error)}</p>}
      </Cell>
    );
  }
  return (
    <Cell title="Push to deploy" className="cell--wide">
      <KeyValues
        items={[
          [
            "URL",
            hook.url ? (
              <CopyableCode value={hook.url} />
            ) : (
              <span>
                <CopyableCode value={hook.path} /> on any domain whose <code>/_webhook/*</code> reaches Bento
              </span>
            ),
          ],
          [
            "Expose via",
            hook.targets.length ? (
              <span className="grid gap-1">
                {hook.targets.map((target) => (
                  <CopyableCode key={target} value={target} />
                ))}
              </span>
            ) : (
              "public listener is off"
            ),
          ],
          ["Content type", <code>application/json</code>],
          [
            "Secret",
            secret ? <CopyableCode value={secret} /> : `hidden (created ${formatRelative(hook.secretCreatedAt)})`,
          ],
        ]}
      />
      <p className="note my-2">
        Edge-routed domains forward <code>/_webhook/*</code> automatically. Otherwise route that path to Bento yourself:
        the loopback address from host nginx, the apps-network address from a Cloudflare Tunnel path rule.
      </p>
      {secret && (
        <p className="note my-2 font-medium">
          Copy the secret now; it is not shown again. Use it as the webhook secret (GitHub, Gitea, Forgejo, Bitbucket),
          the secret token (GitLab), or <code>Authorization: Bearer &lt;secret&gt;</code> from CI.
        </p>
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        <Button variant="outline" onClick={() => setRotateOpen(true)}>
          <KeyRound /> Rotate secret
        </Button>
        <Button variant="outline" onClick={() => setDisableOpen(true)}>
          Disable
        </Button>
      </div>
      {hook.deliveries.length > 0 && (
        <ul className="note mt-3 grid gap-1">
          {hook.deliveries.map((d) => (
            <li key={`${d.at}-${d.deliveryId}-${d.result}`} title={d.detail}>
              {formatRelative(d.at)} · {d.provider} {d.event}
              {d.ref && (
                <>
                  {" "}
                  <code>{d.ref.replace(/^refs\/heads\//, "")}</code>
                </>
              )}{" "}
              → <strong>{d.result}</strong>
              {d.operationId && (
                <>
                  {" "}
                  <code>{d.operationId}</code>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={rotateOpen}
        onOpenChange={setRotateOpen}
        title="Rotate webhook secret?"
        description="The URL stays the same. Deliveries signed with the current secret are rejected until you update it at the git host."
        confirmLabel="Rotate"
        pending={enable.isPending}
        error={enable.error}
        onConfirm={() => enable.mutate()}
      />
      <ConfirmDialog
        open={disableOpen}
        onOpenChange={setDisableOpen}
        title="Disable webhook?"
        description="The URL and secret are destroyed and pushes no longer deploy. Enabling again creates a new URL."
        confirmLabel="Disable"
        destructive
        pending={disable.isPending}
        error={disable.error}
        onConfirm={() => disable.mutate()}
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
