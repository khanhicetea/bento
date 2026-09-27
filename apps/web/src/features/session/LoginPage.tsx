import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, messageOf, setCsrfToken } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function LoginPage() {
  const [password, setPassword] = useState("");
  const queryClient = useQueryClient();
  const login = useMutation({
    mutationFn: api.session.login,
    onSuccess(session) {
      setCsrfToken(session.csrfToken);
      queryClient.setQueryData(keys.session, session);
    },
  });

  function submit(event: FormEvent) {
    event.preventDefault();
    login.mutate(password);
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-6">
      <form
        onSubmit={submit}
        className="grid w-full max-w-sm gap-4 rounded-xl border border-border bg-card p-6 shadow-sm"
      >
        <div className="flex items-center gap-3">
          <img src="/bento-logo-3d.png" alt="" className="size-10 rounded-xl" />
          <div>
            <h1 className="m-0 text-lg font-semibold">Bento control plane</h1>
            <p className="m-0 text-xs text-muted-foreground">Operator sign-in</p>
          </div>
        </div>
        <label className="grid gap-1.5 text-sm">
          Password
          <Input
            type="password"
            autoComplete="current-password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {login.error && <Alert variant="destructive">{messageOf(login.error)}</Alert>}
        <Button type="submit" disabled={login.isPending || password === ""}>
          Sign in
        </Button>
        <p className="m-0 text-xs text-muted-foreground">
          Set the password on the host with <code>bento auth set-password</code>. This management UI is intended only
          for the loopback listener, not an untrusted network.
        </p>
      </form>
    </main>
  );
}
