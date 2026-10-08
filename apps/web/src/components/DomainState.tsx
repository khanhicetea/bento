import { useState, type ReactNode } from "react";
import { Check, CircleHelp, Clock, Copy, Loader, Minus, Square, TriangleAlert, X } from "lucide-react";
import { Link } from "wouter";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Mascot, type Mood } from "./Mascot.tsx";

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

/** Kara cell content: Ben asleep, a caps title, at most a short line and one action. */
export function EmptyState({
  title,
  body,
  action,
  mood = "idle",
}: {
  title: string;
  body?: string;
  action?: ReactNode;
  mood?: Mood;
}) {
  return (
    <div className="empty">
      <Mascot mood={mood} size={88} />
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

/** Cell kinds: fill colour is meaning (DESIGN.md §4). */
export type CellKind = "gohan" | "ume" | "tamago" | "nori" | "kara";
const kindClass: Record<CellKind, string> = {
  gohan: "",
  ume: "cell--alert",
  tamago: "cell--caution",
  nori: "cell--console",
  kara: "cell--muted",
};

/** Mood that mirrors a state badge's tone. */
export function moodOf(state: string): Mood {
  const tone = tones[state] ?? "unknown";
  return tone === "success"
    ? "ok"
    : tone === "info"
      ? "busy"
      : tone === "danger" || tone === "warning"
        ? "alert"
        : "idle";
}

/** One compartment of a bento box. */
export function Cell({
  title,
  icon,
  kind = "gohan",
  action,
  children,
  className = "",
}: {
  title?: string;
  icon?: ReactNode;
  kind?: CellKind;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`cell ${kindClass[kind]} ${className}`}>
      {(title || action) && (
        <div className="cell__title">
          {title && (
            <h2>
              {icon}
              {title}
            </h2>
          )}
          {action}
        </div>
      )}
      {children}
    </section>
  );
}

type Tone = "success" | "info" | "warning" | "danger" | "neutral" | "unknown";
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

const glyphs: Record<Tone, ReactNode> = {
  success: <Check />,
  info: <Loader />,
  warning: <TriangleAlert />,
  danger: <X />,
  neutral: <Square />,
  unknown: <CircleHelp />,
};

/** Glyph + caps word; colour is never the only signal. Unknown states are shown as-is, outlined. */
export function StateBadge({ state, title, label }: { state: string; title?: string; label?: string }) {
  const tone = tones[state] ?? "unknown";
  const glyph = state === "queued" ? <Clock /> : state === "absent" ? <Minus /> : glyphs[tone];
  return (
    <span className={`pill pill--${tone}`} title={title}>
      <span aria-hidden="true" className="contents">
        {glyph}
      </span>
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
