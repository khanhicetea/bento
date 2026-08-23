import { useState } from "react";

export function ConfirmOperationDialog({
  title,
  description,
  confirmation,
  busy,
  onConfirm,
  onClose,
}: {
  title: string;
  description: string;
  confirmation: string;
  busy: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const [value, setValue] = useState("");
  const matches = value === confirmation;

  return (
    <dialog className="modal" open onCancel={(event) => event.preventDefault()}>
      <div className="modal-box">
        <h3 className="text-lg font-bold">{title}</h3>
        <p className="py-3">{description}</p>
        <label className="form-control">
          <span className="label-text">
            Type <strong>{confirmation}</strong> to confirm
          </span>
          <input
            className="input input-bordered mt-2"
            autoFocus
            autoComplete="off"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && matches && !busy) onConfirm();
            }}
          />
        </label>
        <div className="modal-action">
          <button className="btn btn-ghost" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="btn btn-error" disabled={!matches || busy} onClick={onConfirm}>
            {busy ? "Working…" : "Confirm"}
          </button>
        </div>
      </div>
      <button className="modal-backdrop" aria-label="Close" disabled={busy} onClick={onClose} />
    </dialog>
  );
}
