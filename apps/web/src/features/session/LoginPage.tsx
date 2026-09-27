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
    <main className="login-layout">
      <div className="login-aside">
        <div className="login-aside__brand">
          <img src="/bento-logo-3d.png" alt="" />
          bento.
        </div>
        <div>
          <h1>Your stack, in good hands.</h1>
          <p>One place to see what’s running, what changed, and what needs you.</p>
        </div>
        <small>Single-host control plane</small>
      </div>
      <div className="login-main">
        <form onSubmit={submit} className="login-form">
          <h2>Welcome back</h2>
          <p className="login-form__description">Sign in to manage your stack.</p>
          <label className="grid gap-2 text-sm font-medium">
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
          <Button type="submit" className="w-full" disabled={login.isPending || password === ""}>
            {login.isPending ? "Signing in…" : "Sign in"}
          </Button>
          <p className="login-form__help">
            Set your password on the host with <code>bento auth set-password</code>. This management UI is for the
            loopback listener only; do not expose it on an untrusted network.
          </p>
        </form>
      </div>
    </main>
  );
}
