import type { ReactNode } from "react";
import { CheckCircle2, Circle, CircleAlert, CircleDashed, LoaderCircle, TriangleAlert } from "lucide-react";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="grid gap-3 py-6" role="status" aria-label={`Loading ${label}`}>
      <div className="h-5 w-40 animate-pulse rounded bg-muted" />
      <div className="h-24 animate-pulse rounded-lg bg-muted" />
    </div>
  );
}

export function DomainError({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <Alert variant="destructive" className="flex items-center justify-between gap-3">
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
  return <div className="p-10 text-center text-sm text-muted-foreground">{children}</div>;
}

export function EmptyState({
  icon,
  title,
  body,
  action,
}: {
  icon?: ReactNode;
  title: string;
  body?: string;
  action?: ReactNode;
}) {
  return (
    <div className="grid justify-items-center gap-2 p-10 text-center">
      {icon && <span className="text-muted-foreground">{icon}</span>}
      <h3 className="m-0 text-base font-semibold">{title}</h3>
      {body && <p className="m-0 max-w-md text-sm text-muted-foreground">{body}</p>}
      {action}
    </div>
  );
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  section?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex items-start justify-between gap-4 max-sm:flex-col">
      <div className="min-w-0">
        <h1 className="m-0 text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="m-0 mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Page({ children, wide = false }: { children: ReactNode; wide?: boolean }) {
  return (
    <section className={`mx-auto w-full p-4 md:p-6 ${wide ? "max-w-screen-2xl" : "max-w-7xl"}`}>{children}</section>
  );
}

export function Panel({
  title,
  description,
  actions,
  children,
  className = "",
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`mb-6 rounded-xl border bg-card shadow-sm ${className}`}>
      <div className="flex items-start justify-between gap-4 border-b px-5 py-4">
        <div>
          <h2 className="m-0 text-base font-semibold">{title}</h2>
          {description && <p className="m-0 mt-1 text-xs text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      <div className="p-5">{children}</div>
    </section>
  );
}

type Tone = "success" | "info" | "warning" | "danger" | "neutral";
const tones: Record<string, Tone> = {
  healthy: "success",
  succeeded: "success",
  published: "success",
  starting: "info",
  running: "info",
  queued: "info",
  drift: "warning",
  blocked: "warning",
  interrupted: "warning",
  pending: "warning",
  unhealthy: "danger",
  failed: "danger",
  stopped: "neutral",
  absent: "neutral",
  cancelled: "neutral",
  unpublished: "neutral",
};
const labels: Record<string, string> = {
  healthy: "Healthy",
  succeeded: "Succeeded",
  starting: "Starting…",
  running: "Running",
  queued: "Queued",
  unhealthy: "Unhealthy",
  failed: "Failed",
  blocked: "Blocked",
  stopped: "Stopped",
  absent: "Absent",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
  published: "Published",
  unpublished: "Unpublished",
  drift: "Drift",
};
const icons: Record<Tone, typeof Circle> = {
  success: CheckCircle2,
  info: LoaderCircle,
  warning: TriangleAlert,
  danger: CircleAlert,
  neutral: CircleDashed,
};

export function StateBadge({ state, title, label }: { state: string; title?: string; label?: string }) {
  const tone = tones[state] ?? "neutral";
  const Icon = icons[tone];
  return (
    <span className={`status-pill status-${tone}`} title={title}>
      <Icon
        className={`size-3.5 ${state === "running" || state === "queued" || state === "starting" ? "animate-spin" : ""}`}
        aria-hidden="true"
      />
      {label ?? labels[state] ?? state}
    </span>
  );
}

export const StatusPill = StateBadge;

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="grid gap-1.5 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

export function CopyableCode({ value }: { value: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-2">
      <code className="truncate">{value}</code>
      <Button type="button" size="xs" variant="ghost" onClick={() => void navigator.clipboard.writeText(value)}>
        Copy
      </Button>
    </span>
  );
}
