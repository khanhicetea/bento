import { useState, type ReactNode } from "react";
import { Check, Copy } from "lucide-react";
import { Link } from "wouter";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="grid gap-3 py-2" role="status" aria-label={`Loading ${label}`}>
      <div className="skeleton h-6 w-40" />
      <div className="skeleton h-28" />
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
    <div className="empty">
      {icon}
      <strong>{title}</strong>
      {body && <p>{body}</p>}
      {action}
    </div>
  );
}

export function PageHeader({
  title,
  description,
  back,
  actions,
}: {
  title: ReactNode;
  description?: ReactNode;
  back?: { href: string; label: string };
  actions?: ReactNode;
}) {
  return (
    <header className="head">
      <div className="min-w-0">
        {back && (
          <Link href={back.href} className="eyebrow">
            ← {back.label}
          </Link>
        )}
        <h1>{title}</h1>
        {description && <p className="head__sub">{description}</p>}
      </div>
      {actions && <div className="head__actions">{actions}</div>}
    </header>
  );
}

export function Page({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

/** One compartment of a bento box. */
export function Cell({
  title,
  action,
  children,
  className = "",
}: {
  title?: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`cell ${className}`}>
      {(title || action) && (
        <div className="cell__title">
          {title && <h2>{title}</h2>}
          {action}
        </div>
      )}
      {children}
    </section>
  );
}

type Tone = "success" | "info" | "warning" | "danger" | "neutral";
const tones: Record<string, Tone> = {
  healthy: "success",
  succeeded: "success",
  published: "success",
  connected: "success",
  starting: "info",
  running: "info",
  queued: "info",
  drift: "warning",
  blocked: "warning",
  interrupted: "warning",
  pending: "warning",
  partial: "warning",
  unhealthy: "danger",
  failed: "danger",
  stopped: "neutral",
  absent: "neutral",
  cancelled: "neutral",
  unpublished: "neutral",
};
const labels: Record<string, string> = {
  healthy: "Healthy",
  succeeded: "Done",
  starting: "Starting",
  running: "Running",
  queued: "Queued",
  unhealthy: "Unhealthy",
  failed: "Failed",
  partial: "Partial",
  blocked: "Blocked",
  stopped: "Stopped",
  absent: "Off",
  cancelled: "Cancelled",
  interrupted: "Interrupted",
  published: "Public",
  unpublished: "Private",
  drift: "Drift",
};

export function StateBadge({ state, title, label }: { state: string; title?: string; label?: string }) {
  const tone = tones[state] ?? "neutral";
  return (
    <span className={`pill pill--${tone}`} title={title}>
      <span className="dot" aria-hidden="true" />
      {label ?? labels[state] ?? state}
    </span>
  );
}

export const StatusPill = StateBadge;

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {hint && <span className="field__hint">{hint}</span>}
    </label>
  );
}

export function KeyValues({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="kv">
      {items.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function CopyableCode({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <span className="inline-flex max-w-full min-w-0 items-center gap-1">
      <code className="truncate">{value}</code>
      <button
        type="button"
        className="icon-btn size-7!"
        aria-label="Copy"
        onClick={() => {
          void navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        }}
      >
        {copied ? <Check /> : <Copy />}
      </button>
    </span>
  );
}
