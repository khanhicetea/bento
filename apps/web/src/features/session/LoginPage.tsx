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
    <main className="login">
      <form onSubmit={submit} className="login__card">
        <div className="brand">
          <img src="/bento-logo-3d.png" alt="" />
          <span>
            bento<b>.</b>
          </span>
        </div>
        <Input
          type="password"
          aria-label="Password"
          placeholder="Password"
          autoComplete="current-password"
          autoFocus
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {login.error && <Alert variant="destructive">{messageOf(login.error)}</Alert>}
        <Button type="submit" size="lg" className="w-full" disabled={login.isPending || password === ""}>
          {login.isPending ? "Signing in…" : "Sign in"}
        </Button>
        <p className="login__hint">
          Set with <code>bento auth set-password</code>. Loopback only — never expose on an untrusted network.
        </p>
      </form>
    </main>
  );
}
