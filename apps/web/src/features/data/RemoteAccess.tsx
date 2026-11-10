import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { EyeOff, KeyRound, RefreshCw, Waypoints } from "lucide-react";
import { Link } from "wouter";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { CopyButton } from "../../components/CopyButton.tsx";
import { CopyableCode, KeyValues, StateBadge } from "../../components/DomainState.tsx";
import { Switch } from "../../components/Switch.tsx";
import { useOperationMutation } from "../applications/useApplications.ts";
import { Button } from "@/components/ui/button";

/**
 * REMOTE ACCESS section of a MySQL/PostgreSQL service tile (System → Data services): an experimental Tailcat
 * server that forwards the engine port. The address is a credential; it is only shown to browser sessions.
 */
export function ServiceRemoteAccess({ service: s }: { service: T.Service }) {
  const ra = s.remoteAccess;
  const queryClient = useQueryClient();
  const [confirmOn, setConfirmOn] = useState(false);
  const [confirmRotate, setConfirmRotate] = useState(false);
  const change = useOperationMutation((action: T.RemoteAccessAction) => api.services.remote(s.name, action));
  const run = (action: T.RemoteAccessAction, done?: () => void) =>
    change.mutate(action, {
      onSuccess: () => {
        // Intent is already saved; show STARTING / OFF without waiting for the operation.
        void queryClient.invalidateQueries({ queryKey: keys.services });
        done?.();
      },
    });
  return (
    <div className="grid w-full gap-2 border-t border-border pt-3" aria-label={`Remote access of ${s.name}`}>
      <div className="flex w-full items-center justify-between gap-2">
        <span className="label flex items-center gap-2 text-xs">
          <Waypoints className="size-4" aria-hidden="true" /> Remote access
          <span className="tag">Experimental</span>
        </span>
        <span className="flex items-center gap-2">
          <span className="label text-xs">{ra.enabled ? "On" : "Off"}</span>
          <Switch
            checked={ra.enabled}
            label={`Remote access of ${s.name}`}
            disabled={change.isPending}
            onChange={(on) => (on ? setConfirmOn(true) : run("off"))}
          />
        </span>
      </div>
      {ra.enabled && (
        <>
          <div className="flex w-full items-center justify-between gap-2">
            <StateBadge state={ra.state} title={ra.message} />
            <span className="text-xs text-muted-foreground">
              {ra.connections} / {ra.maxConnections} conn
            </span>
          </div>
          <KeyValues
            items={[
              ["Address", ra.address ? <CopyableCode value={ra.address} /> : "—"],
              ["Local port", <code>127.0.0.1:{ra.localPort}</code>],
            ]}
          />
          {ra.state === "failed" && ra.message && <p className="note note--bad">✕ {ra.message}</p>}
          <div className="flex flex-wrap gap-2">
            {ra.command && <CopyButton value={ra.command} label="Copy command" />}
            <Button size="xs" variant="outline" disabled={change.isPending} onClick={() => setConfirmRotate(true)}>
              <RefreshCw /> Rotate address
            </Button>
          </div>
        </>
      )}
      {change.error && <p className="note note--bad">{messageOf(change.error)}</p>}
      <ConfirmDialog
        open={confirmOn}
        onOpenChange={setConfirmOn}
        title={`Turn on remote access for ${s.name}`}
        description="Anyone who has the address and a binding password can reach this database from anywhere. Experimental: the public Tailcat relays have no SLA."
        confirmLabel="Turn on"
        pending={change.isPending}
        error={change.error ?? undefined}
        onConfirm={() => run("on", () => setConfirmOn(false))}
      />
      <ConfirmDialog
        open={confirmRotate}
        onOpenChange={setConfirmRotate}
        title={`Rotate address of ${s.name}`}
        description="The current address stops working and open tunnels are cut. Clients need the new command."
        confirmLabel="Rotate"
        destructive
        pending={change.isPending}
        error={change.error ?? undefined}
        onConfirm={() => run("rotate", () => setConfirmRotate(false))}
      />
    </div>
  );
}

/**
 * Remote access facts of one MySQL/PostgreSQL binding (App → Data): its service's REMOTE state, the forward command,
 * and SHOW LOGIN, which reveals the binding's own login. The login lives only in this mutation's state and is
 * dropped on HIDE LOGIN; the backend journals every reveal.
 */
export function BindingRemoteAccess({
  appId,
  binding,
  service,
}: {
  appId: string;
  binding: T.Binding;
  service: T.Service | undefined;
}) {
  const ra = service?.remoteAccess;
  const login = useMutation({ mutationFn: () => api.apps.bindingLogin(appId, binding.id) });
  return (
    <div className="grid gap-3 border-t border-border pt-3">
      <KeyValues
        items={[
          [
            "Remote",
            ra?.enabled ? (
              <StateBadge state={ra.state} title={ra.message} />
            ) : (
              <StateBadge state="absent" label="Off" />
            ),
          ],
        ]}
      />
      {!ra?.enabled && (
        <p className="note">
          Turn on under <Link href="/system">System → Data services</Link>.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {ra?.command && <CopyButton value={ra.command} label="Copy command" />}
        {login.data ? (
          <Button size="xs" variant="outline" onClick={() => login.reset()}>
            <EyeOff /> Hide login
          </Button>
        ) : (
          <Button size="xs" variant="outline" disabled={login.isPending} onClick={() => login.mutate()}>
            <KeyRound /> {login.isPending ? "Showing" : "Show login"}
          </Button>
        )}
      </div>
      {login.data && <LoginFacts login={login.data} />}
      {login.error && <p className="note note--bad">{messageOf(login.error)}</p>}
    </div>
  );
}

function LoginFacts({ login }: { login: T.BindingLogin }) {
  return (
    <div className="grid gap-2">
      <KeyValues
        items={[
          ["Host", <CopyableCode value={`${login.host}:${login.port}`} />],
          ["User", <CopyableCode value={login.username} />],
          ["Password", <CopyableCode value={login.password} />],
          ["Databases", login.databases.length ? login.databases.join(", ") : "—"],
        ]}
      />
      <div className="flex flex-wrap gap-2">
        {login.urls.map((u) => (
          <CopyButton
            key={u.database}
            value={u.url}
            label={login.urls.length > 1 ? `Copy URL ${u.database}` : "Copy URL"}
          />
        ))}
      </div>
      <p className="note note--caution">▲ Address and password grant access. Share neither.</p>
    </div>
  );
}
