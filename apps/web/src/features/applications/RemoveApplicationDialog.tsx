import { useState, type FormEvent } from "react";
import type { Application } from "@bento/shared";

type RemoveApplicationDialogProps = {
  application: Application;
  error: string | null;
  removing: boolean;
  onClose: () => void;
  onRemove: (slug: string, confirmation: string) => Promise<Application>;
};

export function RemoveApplicationDialog({
  application,
  error,
  removing,
  onClose,
  onRemove,
}: RemoveApplicationDialogProps) {
  const [confirmation, setConfirmation] = useState("");
  const expected = `delete ${application.slug}`;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      await onRemove(application.slug, confirmation);
      onClose();
    } catch {
      // The TanStack mutation exposes the sanitized oRPC error in the dialog.
    }
  }

  return (
    <dialog className="modal" open onCancel={(event) => event.preventDefault()}>
      <div className="modal-box remove-app-box">
        <h2>Remove {application.slug}?</h2>
        <div className="alert alert-warning">
          Runtime configuration will be removed. The application home and database data will be
          retained for operator-controlled cleanup.
        </div>
        {error && <div className="alert alert-error">{error}</div>}
        <form onSubmit={(event) => void submit(event)}>
          <label>
            <span className="label-text">
              Type <code>{expected}</code> to confirm
            </span>
            <input
              className="input input-bordered w-full"
              autoFocus
              value={confirmation}
              disabled={removing}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </label>
          <div className="modal-action">
            <button type="button" className="btn btn-ghost" disabled={removing} onClick={onClose}>
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-error"
              disabled={removing || confirmation !== expected}
            >
              {removing && <span className="loading loading-spinner loading-xs" />}
              Remove application
            </button>
          </div>
        </form>
      </div>
      <button className="modal-backdrop" aria-label="Close" disabled={removing} onClick={onClose} />
    </dialog>
  );
}
