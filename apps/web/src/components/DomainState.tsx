import type { ReactNode } from "react";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";

export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="flex min-h-[45vh] items-center justify-center gap-3 opacity-70">
      <Spinner className="size-8" aria-label={`Loading ${label}`} /> Loading {label}…
    </div>
  );
}

export function DomainError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <Alert variant="destructive" className="grid-cols-[minmax(0,1fr)_auto] justify-between gap-x-3">
      <span>{message}</span>
      {onRetry && (
        <Button size="sm" variant="outline" onClick={onRetry}>
          Retry
        </Button>
      )}
    </Alert>
  );
}

export function EmptyPanel({ children }: { children: ReactNode }) {
  return <div className="p-10 text-center text-sm opacity-60">{children}</div>;
}

export function PageHeader({
  section,
  title,
  description,
  actions,
}: {
  section: string;
  title: string;
  description: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex items-end justify-between gap-6 max-[760px]:flex-col max-[760px]:items-stretch">
      <div>
        <p className="mb-2 text-[0.68rem] font-semibold tracking-[0.16em] text-muted-foreground uppercase">
          Control plane / {section}
        </p>
        <h2 className="m-0 text-[clamp(1.7rem,3vw,2.35rem)] tracking-tight">{title}</h2>
        <p className="m-0 mt-2 max-w-[680px] text-sm text-muted-foreground">{description}</p>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Page({ children }: { children: ReactNode }) {
  return <section className="mx-auto w-full max-w-[1500px] p-[clamp(1rem,2.5vw,2.5rem)]">{children}</section>;
}

export function Panel({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="mb-6 rounded-xl border border-border bg-card shadow-sm">
      <div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
        <div>
          <h3 className="m-0 text-base font-semibold">{title}</h3>
          {description && <p className="m-0 mt-1 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      <div className="p-5">{children}</div>
    </div>
  );
}

const stateTone: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  healthy: "default",
  starting: "secondary",
  unhealthy: "destructive",
  failed: "destructive",
  blocked: "destructive",
  stopped: "outline",
  absent: "outline",
  succeeded: "default",
  running: "secondary",
  queued: "outline",
  cancelled: "outline",
  interrupted: "destructive",
};

export function StateBadge({ state, title }: { state: string; title?: string }) {
  return (
    <Badge variant={stateTone[state] ?? "outline"} title={title}>
      {state}
    </Badge>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}
