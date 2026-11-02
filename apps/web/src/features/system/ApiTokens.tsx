import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus, Trash2, TriangleAlert } from "lucide-react";
import { api, messageOf, type T } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { ConfirmDialog } from "../../components/ConfirmDialog.tsx";
import { Cell, CopyableCode, DomainError, Field } from "../../components/DomainState.tsx";
import { formatRelative } from "../../lib/format.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";

const scopeLabels: Record<T.TokenScope, string> = {
  read: "Read · status, apps, logs",
  operate: "Operate · + start, deploy, back up",
  admin: "Admin · + create, configure",
};

/** API tokens for MCP clients and scripts (System → Overview). */
export function ApiTokens() {
  return (
    <div className="box box--2">
      <TokenList />
      <NewToken />
    </div>
  );
}

function TokenList() {
  const queryClient = useQueryClient();
  const query = useQuery({ queryKey: keys.tokens, queryFn: ({ signal }) => api.tokens.list(signal) });
  const [target, setTarget] = useState<T.APIToken | null>(null);
  const revoke = useMutation({
    mutationFn: (id: string) => api.tokens.revoke(id),
    onSuccess: () => {
      setTarget(null);
      void queryClient.invalidateQueries({ queryKey: keys.tokens });
    },
  });
  const tokens = query.data?.tokens ?? [];
  return (
    <Cell title="API tokens" icon={<KeyRound />}>
      {query.error ? (
        <DomainError message={messageOf(query.error)} onRetry={() => void query.refetch()} />
      ) : tokens.length === 0 ? (
        <p className="note">No tokens</p>
      ) : (
        <div className="rows rows--lined">
          {tokens.map((token) => (
            <div key={token.id} className="row">
              <span className="row__main">
                <strong>
                  {token.name} <span className="font-normal text-muted-foreground">· {token.scope.toUpperCase()}</span>
                </strong>
                <small>
                  {token.expiresAt ? `expires ${formatRelative(token.expiresAt)}` : "never expires"} · used{" "}
                  {token.lastUsedAt ? formatRelative(token.lastUsedAt) : "—"}
                </small>
              </span>
              <Button variant="danger" onClick={() => setTarget(token)}>
                <Trash2 /> Revoke
              </Button>
            </div>
          ))}
        </div>
      )}
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
        title={`Revoke ${target?.name ?? "token"}?`}
        description="Clients using this token lose access at once. Operations it started are kept."
        destructive
        confirmLabel="Revoke"
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => target && revoke.mutate(target.id)}
      />
    </Cell>
  );
}

function NewToken() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [scope, setScope] = useState<T.TokenScope>("read");
  const [days, setDays] = useState("30");
  const [current, setCurrent] = useState("");
  const create = useMutation({
    mutationFn: () =>
      api.tokens.create({ name: name.trim(), scope, expiresInDays: Number(days), currentPassword: current }),
    onSuccess: () => {
      setName("");
      setCurrent("");
      void queryClient.invalidateQueries({ queryKey: keys.tokens });
    },
  });
  return (
    <Cell
      title="New token"
      icon={<Plus />}
      kind={create.data ? "tamago" : "gohan"}
      action={
        <Button type="submit" size="xs" disabled={!name.trim() || !current || create.isPending}>
          {create.isPending ? "Creating" : "Create"}
        </Button>
      }
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <div className="grid gap-4">
        {create.data && (
          <div className="grid gap-2">
            <CopyableCode value={create.data.token} />
            <p className="notice notice--warning">
              <TriangleAlert />
              <strong>Copy token now</strong>
              <span>
                It is not shown again. Anyone with it gets {create.data.scope} access to this stack through the
                management listener.
              </span>
            </p>
          </div>
        )}
        <Field label="Name">
          <Input
            value={name}
            placeholder="claude"
            spellCheck={false}
            maxLength={48}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <div className="grid-2">
          <Field label="Scope">
            <NativeSelect
              className="w-full"
              value={scope}
              onChange={(event) => setScope(event.target.value as T.TokenScope)}
            >
              {(Object.keys(scopeLabels) as T.TokenScope[]).map((value) => (
                <option key={value} value={value}>
                  {scopeLabels[value]}
                </option>
              ))}
            </NativeSelect>
          </Field>
          <Field label="Expires">
            <NativeSelect className="w-full" value={days} onChange={(event) => setDays(event.target.value)}>
              <option value="7">7 days</option>
              <option value="30">30 days</option>
              <option value="90">90 days</option>
              <option value="365">365 days</option>
              <option value="0">Never</option>
            </NativeSelect>
          </Field>
        </div>
        <Field label="Current password" hint="Required to mint a token">
          <Input
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(event) => setCurrent(event.target.value)}
          />
        </Field>
        <p className="note">
          MCP: <code>POST /mcp</code> with <code>Authorization: Bearer …</code>, or <code>bento mcp</code> over SSH.
        </p>
        {create.error && <p className="note note--bad">{messageOf(create.error)}</p>}
      </div>
    </Cell>
  );
}
