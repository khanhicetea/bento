import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Application } from "@bento/shared";
import { orpc } from "../../api/client.ts";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";

export function ApplicationPublicKeyDialog({
  application,
  onClose,
}: {
  application: Application;
  onClose: () => void;
}) {
  const key = useQuery(orpc.applications.publicKey.queryOptions({ input: { slug: application.slug } }));
  const [copyStatus, setCopyStatus] = useState("");

  async function copy() {
    if (!key.data) return;
    try {
      await navigator.clipboard.writeText(key.data.publicKey);
      setCopyStatus("Copied to clipboard");
    } catch {
      setCopyStatus("Copy failed. Select and copy the key below instead.");
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-[600px]">
        <DialogHeader>
          <DialogTitle>Deploy key for {application.slug}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          Add this public key as a read-only deploy key in your repository settings. Never upload the private key.
        </p>
        {key.isPending && (
          <p className="flex items-center gap-2">
            <Spinner /> Loading public key…
          </p>
        )}
        {key.error && <Alert variant="destructive">Public key unavailable. Check the app key on the server.</Alert>}
        {key.data && (
          <>
            <textarea
              readOnly
              aria-label="SSH public key"
              value={key.data.publicKey}
              onFocus={(event) => event.currentTarget.select()}
              className="w-full resize-none break-all rounded-md border border-border bg-muted p-3 font-mono text-xs"
              rows={4}
            />
            <Button onClick={() => void copy()}>Copy public key</Button>
            <p role="status" className="text-sm text-muted-foreground">
              {copyStatus}
            </p>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
