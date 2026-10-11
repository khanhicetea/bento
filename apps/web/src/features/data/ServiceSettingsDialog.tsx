import { useState } from "react";
import { ApiError, api, messageOf, type T } from "../../api/client.ts";
import { Field } from "../../components/DomainState.tsx";
import { useOperationMutation } from "../applications/useApplications.ts";
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

/** Tunes a MySQL/PostgreSQL service; saving recreates its container on the same volume. */
export function ServiceSettingsDialog({
  service,
  open,
  onOpenChange,
}: {
  service: T.Service;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        {open && <SettingsForm service={service} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  );
}

function SettingsForm({ service, onDone }: { service: T.Service; onDone: () => void }) {
  // Mounted only while open, so the draft starts from the saved settings each time.
  const [draft, setDraft] = useState<Record<string, string>>(() => ({ ...service.settings }));
  const save = useOperationMutation(() => api.services.configure(service.name, { settings: draft }));
  const fieldError = (key: string) =>
    save.error instanceof ApiError ? save.error.fields.find((f) => f.field === `settings.${key}`)?.message : undefined;
  const changed = service.tunables.some((t) => (draft[t.key] ?? "") !== (service.settings[t.key] ?? ""));
  return (
    <form
      className="grid gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(undefined, { onSuccess: onDone });
      }}
    >
      <DialogHeader>
        <DialogTitle>Tune {service.name}</DialogTitle>
        <DialogDescription className="notice notice--warning notice--plain text-foreground">
          Saving restarts {service.name}. Apps lose their connection for a few seconds; data stays on the volume.
        </DialogDescription>
      </DialogHeader>
      <div className="grid gap-3 sm:grid-cols-2">
        {service.tunables.map((t) => {
          const error = fieldError(t.key);
          return (
            <Field
              key={t.key}
              label={t.label}
              hint={
                error ? (
                  <span className="text-destructive">✕ {error}</span>
                ) : (
                  <>
                    <code className="tracking-normal normal-case">{t.key}</code> · {t.help} · {t.min}–{t.max}
                  </>
                )
              }
            >
              <Input
                value={draft[t.key] ?? ""}
                placeholder={`${t.default} (default)`}
                inputMode={t.kind === "int" ? "numeric" : "text"}
                autoComplete="off"
                spellCheck={false}
                aria-invalid={error !== undefined}
                onChange={(event) => setDraft({ ...draft, [t.key]: event.target.value })}
              />
            </Field>
          );
        })}
      </div>
      <p className="note">Sizes take K, M or G. Leave empty for the engine default.</p>
      {save.error && !(save.error instanceof ApiError && save.error.fields.length > 0) && (
        <Alert variant="destructive">{messageOf(save.error)}</Alert>
      )}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!changed || save.isPending}>
          {save.isPending ? "Saving…" : "Save & restart"}
        </Button>
      </DialogFooter>
    </form>
  );
}
