import { useState, type FormEvent } from "react";
import type { Application } from "@bento/shared";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert } from "@/components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

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
    <Dialog open onOpenChange={(open) => !open && !removing && onClose()}>
      <DialogContent
        className="remove-app-box max-h-[calc(100vh-2rem)] overflow-y-auto"
        showCloseButton={!removing}
      >
        <DialogHeader>
          <DialogTitle>Remove {application.slug}?</DialogTitle>
        </DialogHeader>
        <Alert className="border-amber-500/40 bg-amber-500/10">
          Runtime configuration will be removed. The application home and database data will be
          retained for operator-controlled cleanup.
        </Alert>
        {error && <Alert variant="destructive">{error}</Alert>}
        <form onSubmit={(event) => void submit(event)}>
          <label>
            <span className="label-text">
              Type <code>{expected}</code> to confirm
            </span>
            <Input
              className="w-full"
              autoFocus
              value={confirmation}
              disabled={removing}
              onChange={(event) => setConfirmation(event.target.value)}
            />
          </label>
          <DialogFooter>
            <Button type="button" variant="ghost" disabled={removing} onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant="destructive"
              disabled={removing || confirmation !== expected}
            >
              {removing && <Spinner />}
              Remove application
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
