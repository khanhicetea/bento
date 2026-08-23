export function DomainLoading({ label }: { label: string }) {
  return (
    <div className="loading-state">
      <span className="loading loading-spinner loading-lg" /> Loading {label}…
    </div>
  );
}

export function DomainError({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="alert alert-error domain-alert">
      <span>{message}</span>
      <button className="btn btn-sm btn-outline" onClick={onRetry}>
        Retry
      </button>
    </div>
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
