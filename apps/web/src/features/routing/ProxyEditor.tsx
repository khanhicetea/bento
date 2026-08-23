import { useState, type FormEvent } from "react";
import type { RoutingProxy, SaveRoutingProxyInput } from "@bento/shared";
import { Network } from "lucide-react";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";

type Props = {
  proxy: RoutingProxy | null;
  error: string | null;
  saving: boolean;
  onClose: () => void;
  onSave: (input: SaveRoutingProxyInput) => Promise<RoutingProxy>;
};

export function ProxyEditor({ proxy, error, saving, onClose, onSave }: Props) {
  const [tls, setTls] = useState<SaveRoutingProxyInput["tls"]>(proxy?.tls ?? "shared");
  const creating = proxy === null;

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const lines = (value: FormDataEntryValue | null) =>
      String(value ?? "")
        .split(/[\n,]/)
        .map((item) => item.trim())
        .filter(Boolean);
    try {
      await onSave({
        operation: creating ? "create" : "update",
        name: String(form.get("name") ?? "").trim(),
        domain: String(form.get("domain") ?? "").trim(),
        aliases: lines(form.get("aliases")),
        upstreams: lines(form.get("upstreams")),
        tls,
        tlsCertificatePath:
          tls === "external" ? String(form.get("tlsCertificatePath") ?? "").trim() : undefined,
        tlsKeyPath: tls === "external" ? String(form.get("tlsKeyPath") ?? "").trim() : undefined,
        accessLog: form.get("accessLog") === "on",
      });
      onClose();
    } catch {
      // Mutation state displays the sanitized server error.
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !saving && onClose()}>
      <DialogContent
        className="max-h-[calc(100vh-2rem)] max-w-[760px] overflow-y-auto"
        showCloseButton={!saving}
      >
        <DialogHeader>
          <div className="flex items-center gap-3">
            <span className="grid size-10 place-items-center rounded-xl bg-primary/10 text-primary">
              <Network className="size-5" aria-hidden="true" />
            </span>
            <div>
              <DialogTitle>{creating ? "Add reverse proxy" : `Edit ${proxy.name}`}</DialogTitle>
              <DialogDescription className="mt-1">
                Route one or more public domains to validated HTTP or HTTPS upstreams.
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        {error && <Alert variant="destructive">{error}</Alert>}
        <form onSubmit={(event) => void submit(event)}>
          <fieldset disabled={saving} className="grid grid-cols-2 gap-4 max-[620px]:grid-cols-1">
            <label>
              <FieldLabel>Name</FieldLabel>
              <Input
                name="name"
                required
                maxLength={63}
                pattern="[a-z0-9][a-z0-9-]*"
                defaultValue={proxy?.name ?? ""}
                readOnly={!creating}
              />
            </label>
            <label>
              <FieldLabel>Primary domain</FieldLabel>
              <Input
                name="domain"
                required
                defaultValue={proxy?.domain ?? ""}
                placeholder="service.example.com"
              />
            </label>
            <label className="col-span-full">
              <FieldLabel>Aliases (comma-separated)</FieldLabel>
              <Input
                name="aliases"
                defaultValue={proxy?.aliases.join(", ") ?? ""}
                placeholder="www.service.example.com"
              />
            </label>
            <label className="col-span-full">
              <FieldLabel>Upstreams (one per line)</FieldLabel>
              <Textarea
                name="upstreams"
                required
                rows={4}
                defaultValue={proxy?.upstreams.join("\n") ?? "http://127.0.0.1:3000"}
                placeholder="http://127.0.0.1:3000"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                All upstreams must use the same scheme, path, and query. Credentials and fragments
                are rejected.
              </p>
            </label>
            <label>
              <FieldLabel>TLS mode</FieldLabel>
              <NativeSelect
                className="w-full"
                value={tls}
                onChange={(event) => setTls(event.target.value as SaveRoutingProxyInput["tls"])}
              >
                <NativeSelectOption value="shared">Shared starter</NativeSelectOption>
                <NativeSelectOption value="self-ca">Private CA</NativeSelectOption>
                <NativeSelectOption value="acme">ACME</NativeSelectOption>
                <NativeSelectOption value="external">External certificate</NativeSelectOption>
              </NativeSelect>
            </label>
            <label className="flex items-center gap-2 self-end pb-2">
              <Checkbox name="accessLog" defaultChecked={proxy?.accessLog ?? false} />
              <span className="text-sm font-medium">Enable access logs</span>
            </label>
            {tls === "external" && (
              <>
                <label>
                  <FieldLabel>Certificate path</FieldLabel>
                  <Input
                    name="tlsCertificatePath"
                    required
                    defaultValue={proxy?.tlsCertificatePath ?? ""}
                    placeholder="/path/to/fullchain.pem"
                  />
                </label>
                <label>
                  <FieldLabel>Private key path</FieldLabel>
                  <Input
                    name="tlsKeyPath"
                    required
                    defaultValue={proxy?.tlsKeyPath ?? ""}
                    placeholder="/path/to/privkey.pem"
                  />
                </label>
              </>
            )}
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="ghost" disabled={saving} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving && <Spinner />}
              {creating ? "Add proxy" : "Save changes"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function FieldLabel({ children }: { children: string }) {
  return <span className="mb-1.5 block text-sm font-semibold">{children}</span>;
}
