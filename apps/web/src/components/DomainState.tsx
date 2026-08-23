import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="loading-state">
      <Spinner className="size-8" aria-label={`Loading ${label}`} /> Loading {label}…
    </div>
  );
}

export function DomainError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <Alert variant="destructive" className="domain-alert">
      <span>{message}</span>
      <Button size="sm" variant="outline" onClick={onRetry}>
        Retry
      </Button>
    </Alert>
  );
}

export function StackNotReady({ stackRoot, error }: { stackRoot: string; error?: string }) {
  return (
    <div className="hero">
      <div>
        <p className="eyebrow">STACK NOT READY</p>
        <h2>Initialize this stack to view this domain.</h2>
        <p>
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
  return <div className="empty">{children}</div>;
}
