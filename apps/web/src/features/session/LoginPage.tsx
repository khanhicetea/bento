import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, messageOf, setCsrfToken } from "../../api/client.ts";
import { keys } from "../../api/keys.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { BentoMark, Mascot } from "../../components/Mascot.tsx";
import { Lock } from "lucide-react";

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
      <div className="login__box">
        <div className="login__hero">
          <Mascot mood={login.error ? "alert" : login.isPending ? "busy" : "ok"} size={132} />
          <div className="brand">
            <BentoMark size={44} />
            <span>
              bento<b>.</b>
            </span>
          </div>
        </div>
        <form onSubmit={submit} className="login__card">
          <label className="field">
            <span>Password</span>
            <Input
              type="password"
              autoComplete="current-password"
              autoFocus
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </label>
          {login.error && <Alert variant="destructive">{messageOf(login.error)}</Alert>}
          <Button type="submit" size="lg" className="w-full" disabled={login.isPending || password === ""}>
            {login.isPending ? "Signing in" : "Sign in"}
          </Button>
          <p className="login__hint">
            <Lock className="size-3.5" aria-hidden="true" />
            Loopback only · never expose · set with <code>bento auth set-password</code>
          </p>
        </form>
      </div>
    </main>
  );
}
