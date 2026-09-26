import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="flex min-h-[45vh] items-center justify-center gap-3 opacity-70">
      <Spinner className="size-8" aria-label={`Loading ${label}`} /> Loading {label}…
    </div>
  );
}

export function DomainError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <Alert variant="destructive" className="grid-cols-[minmax(0,1fr)_auto] justify-between gap-x-3">
      <span>{message}</span>
      <Button size="sm" variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </Alert>
  );
}

export function StackNotReady({ stackRoot, error }: { stackRoot: string; error?: string }) {
  return (
    <div className="flex items-end justify-between gap-8 rounded-[1.25rem] bg-gradient-to-br from-sidebar to-primary p-[clamp(1.4rem,4vw,2.7rem)] text-sidebar-foreground shadow-lg max-[760px]:block">
      <div>
        <p className="m-0 text-[0.64rem] font-bold tracking-[0.14em] text-sidebar-foreground/70">STACK NOT READY</p>
        <h2 className="my-2 text-[clamp(1.5rem,4vw,2.5rem)]">Initialize this stack to view this domain.</h2>
        <p className="max-w-[680px] opacity-70">
          {error ?? (
            <>
              Run <code>bento init</code> for <code>{stackRoot}</code>, then refresh.
            </>
          )}
        </p>
      </div>
    </div>
  );
}

export function EmptyPanel({ children }: { children: string }) {
  return <div className="p-12 text-center opacity-60">{children}</div>;
}
