import { useState, type FormEvent } from "react";
import type { RoutingProxy } from "@bento/shared";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";

type Props = {
  proxy: RoutingProxy;
  error: string | null;
  removing: boolean;
  onClose: () => void;
  onRemove: (name: string, confirmation: string) => Promise<RoutingProxy>;
};

export function RemoveProxyDialog({ proxy, error, removing, onClose, onRemove }: Props) {
  const [confirmation, setConfirmation] = useState("");
  const expected = `delete ${proxy.name}`;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      await onRemove(proxy.name, confirmation);
      onClose();
    } catch {
      // Mutation state displays the sanitized server error.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !removing && onClose()}>
      <DialogContent className="max-w-[560px]" showCloseButton={!removing}>
        <DialogHeader>
          <DialogTitle>Delete {proxy.name}?</DialogTitle>
        </DialogHeader>
        <Alert className="border-amber-500/40 bg-amber-500/10">
          The proxy vhost and its domain claims will be removed. The upstream service is not
          modified.
        </Alert>
        {error && <Alert variant="destructive">{error}</Alert>}
        <form onSubmit={(event) => void submit(event)}>
          <label>
            <span className="mb-1.5 mt-3 block font-semibold">
              Type <code>{expected}</code> to confirm
            </span>
            <Input
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
              {removing && <Spinner />} Delete reverse proxy
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
