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
import { TriangleAlert } from "lucide-react";

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
          <DialogTitle className={destructive ? "flex items-center gap-2 text-destructive" : undefined}>
            {destructive && <TriangleAlert className="size-4" aria-hidden="true" />}
            {title}
          </DialogTitle>
          <DialogDescription
            className={destructive ? "notice notice--warning notice--plain text-foreground" : undefined}
          >
            {description}
          </DialogDescription>
        </DialogHeader>
        {phrase && (
          <label className="field">
            <span>
              Type{" "}
              <code className="rounded-md bg-[var(--ume)] px-1.5 py-0.5 tracking-normal normal-case text-[var(--ume-ink)]">
                {phrase}
              </code>
            </span>
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
