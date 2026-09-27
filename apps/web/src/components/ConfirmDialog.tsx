import { useEffect, useState } from "react";
import { messageOf } from "../api/client.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "Confirm",
  phrase,
  destructive = false,
  pending = false,
  error,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel?: string;
  phrase?: string;
  destructive?: boolean;
  pending?: boolean;
  error?: unknown;
  onConfirm: (typed: string) => void;
}) {
  const [typed, setTyped] = useState("");
  useEffect(() => {
    if (!open) setTyped("");
  }, [open]);
  function close(next: boolean) {
    if (!next) setTyped("");
    onOpenChange(next);
  }
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {phrase && (
          <label className="grid gap-2 text-sm font-medium">
            Type <code>{phrase}</code> to confirm
            <Input value={typed} onChange={(event) => setTyped(event.target.value)} autoComplete="off" autoFocus />
          </label>
        )}
        {error !== undefined && error !== null && <Alert variant="destructive">{messageOf(error)}</Alert>}
        <DialogFooter>
          <Button variant="outline" onClick={() => close(false)}>
            Cancel
          </Button>
          <Button
            variant={destructive ? "destructive" : "default"}
            disabled={pending || (phrase !== undefined && typed !== phrase)}
            onClick={() => onConfirm(typed)}
          >
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
