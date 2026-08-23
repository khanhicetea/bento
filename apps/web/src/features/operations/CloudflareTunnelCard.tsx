import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Cloud, KeyRound } from "lucide-react";
import { useState, type FormEvent } from "react";
import { orpc } from "../../api/client.ts";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";

export function CloudflareTunnelCard({ configured }: { configured: boolean }) {
  const queryClient = useQueryClient();
  const [token, setToken] = useState("");
  const setup = useMutation(
    orpc.operations.setupCloudflareTunnel.mutationOptions({
      onSuccess() {
        setToken("");
        void queryClient.invalidateQueries({ queryKey: orpc.operations.overview.key() });
      },
    }),
  );

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = token.trim();
    if (value) setup.mutate({ token: value });
  }

  return (
    <article className="mt-8 rounded-2xl border border-border bg-card p-5 text-card-foreground shadow-sm md:p-6">
      <div className="flex items-start justify-between gap-4 max-[640px]:flex-col">
        <div className="flex items-start gap-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-orange-500/10 text-orange-600 dark:text-orange-400">
            <Cloud className="size-4" aria-hidden="true" />
          </span>
          <div>
            <p className="m-0 text-[0.68rem] font-semibold uppercase tracking-[0.14em] text-muted-foreground">
              Private ingress
            </p>
            <h3 className="m-0 mt-1 text-lg font-semibold tracking-tight">Cloudflare Tunnel</h3>
            <p className="m-0 mt-1 max-w-[760px] text-sm text-muted-foreground">
              Run cloudflared in Nginx&apos;s network namespace. Configure Cloudflare public
              hostnames to use Nginx origins for Bento applications or the loopback-safe Bento web
              server.
            </p>
          </div>
        </div>
        <Badge className={configured ? "bg-emerald-600 text-white" : ""} variant="outline">
          {configured ? "Configured" : "Not configured"}
        </Badge>
      </div>

      {setup.error && (
        <Alert className="mt-5" variant="destructive">
          {messageOf(setup.error)}
        </Alert>
      )}
      {setup.data && (
        <Alert className="mt-5 border-emerald-600/40 bg-emerald-600/10 text-emerald-700 dark:text-emerald-400">
          <CheckCircle2 aria-hidden="true" />
          {setup.data.message}
        </Alert>
      )}

      <form className="mt-6 flex items-end gap-3 max-[640px]:flex-col" onSubmit={submit}>
        <div className="w-full min-w-0 flex-1">
          <label htmlFor="cloudflare-tunnel-token" className="text-sm font-semibold">
            Tunnel token
          </label>
          <p className="m-0 mt-1 text-xs text-muted-foreground">
            The token is stored in a private stack file and is never returned by the API.
          </p>
          <div className="relative mt-3">
            <KeyRound
              className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              id="cloudflare-tunnel-token"
              className="pl-9"
              type="password"
              autoComplete="off"
              value={token}
              disabled={setup.isPending}
              placeholder={configured ? "Paste a replacement token" : "Paste tunnel token"}
              onChange={(event) => setToken(event.target.value)}
            />
          </div>
        </div>
        <Button
          className="max-[640px]:w-full"
          type="submit"
          disabled={setup.isPending || !token.trim()}
        >
          {setup.isPending ? <Spinner /> : <Cloud className="size-4" aria-hidden="true" />}
          {setup.isPending ? "Starting…" : configured ? "Update token" : "Set up tunnel"}
        </Button>
      </form>
    </article>
  );
}

function messageOf(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
