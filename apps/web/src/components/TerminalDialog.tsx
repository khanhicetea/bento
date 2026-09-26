import { useEffect, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type ConnectionState = "connecting" | "connected" | "closed" | "error";

export function TerminalDialog({
  target,
  onClose,
}: {
  target: { app: string } | { service: string };
  onClose: () => void;
}) {
  const name = "app" in target ? target.app : target.service;
  const [terminalElement, setTerminalElement] = useState<HTMLDivElement | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>("connecting");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const container = terminalElement;
    if (!container) return;

    let active = true;
    let sessionId: string | undefined;
    let inputBuffer = "";
    let inputTimer: ReturnType<typeof setTimeout> | undefined;
    let sendChain = Promise.resolve();
    const outputAbort = new AbortController();

    setConnectionState("connecting");
    const terminal = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: '"JetBrains Mono", "SFMono-Regular", Consolas, monospace',
      fontSize: 14,
      scrollback: 5_000,
      theme: {
        background: "#09090b",
        foreground: "#fafafa",
        cursor: "#fafafa",
        selectionBackground: "#3f3f46",
      },
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(container);
    terminal.writeln(`\x1b[90mConnecting to ${name}…\x1b[0m`);

    function postMessage(message: object) {
      const id = sessionId;
      if (!active || !id) return;
      sendChain = sendChain
        .then(async () => {
          const response = await fetch(`/api/terminal/${id}/input`, {
            method: "POST",
            headers: { "content-type": "application/json" },
            body: JSON.stringify(message),
          });
          if (!response.ok) throw new Error("Terminal input was rejected");
        })
        .catch(() => {
          if (active) setConnectionState("error");
        });
    }

    function flushInput() {
      inputTimer = undefined;
      while (inputBuffer) {
        const data = inputBuffer.slice(0, 8_192);
        inputBuffer = inputBuffer.slice(data.length);
        postMessage({ type: "input", data });
      }
    }

    function sendResize() {
      postMessage({ type: "resize", cols: terminal.cols, rows: terminal.rows });
    }

    function fit() {
      if (!active) return;
      try {
        fitAddon.fit();
        sendResize();
      } catch {
        // The dialog can disappear while a queued resize callback is running.
      }
    }

    const resizeObserver = new ResizeObserver(fit);
    resizeObserver.observe(container);
    const input = terminal.onData((data) => {
      if (!active || !sessionId) return;
      inputBuffer += data;
      if (inputTimer === undefined) inputTimer = setTimeout(flushInput, 12);
    });

    async function connect() {
      try {
        const createResponse = await fetch("/api/terminal", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify(target),
          signal: outputAbort.signal,
        });
        const createResult: unknown = await createResponse.json();
        if (
          !createResponse.ok ||
          !createResult ||
          typeof createResult !== "object" ||
          !("id" in createResult) ||
          typeof createResult.id !== "string"
        ) {
          const message =
            createResult &&
            typeof createResult === "object" &&
            "error" in createResult &&
            typeof createResult.error === "string"
              ? createResult.error
              : "Unable to create shell session";
          throw new Error(message);
        }
        if (!active) return;
        sessionId = createResult.id;

        const outputResponse = await fetch(`/api/terminal/${sessionId}/output`, {
          signal: outputAbort.signal,
          headers: { accept: "application/octet-stream" },
        });
        if (!outputResponse.ok || !outputResponse.body) throw new Error("Unable to attach shell output");
        if (!active) return;

        setConnectionState("connected");
        terminal.clear();
        fit();
        terminal.focus();

        const reader = outputResponse.body.getReader();
        while (active) {
          const { done, value } = await reader.read();
          if (done) break;
          terminal.write(value);
        }
        if (active) setConnectionState("closed");
      } catch (error) {
        if (!active || outputAbort.signal.aborted) return;
        terminal.writeln(
          `\r\n\x1b[31m${error instanceof Error ? error.message : "Unable to connect to the shell."}\x1b[0m`,
        );
        setConnectionState("error");
      }
    }

    void connect();

    return () => {
      active = false;
      if (inputTimer !== undefined) clearTimeout(inputTimer);
      resizeObserver.disconnect();
      input.dispose();
      outputAbort.abort();
      if (sessionId) {
        void fetch(`/api/terminal/${sessionId}`, { method: "DELETE", keepalive: true }).catch(() => undefined);
      }
      terminal.dispose();
    };
  }, [name, attempt, terminalElement]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="flex h-[min(82vh,760px)] w-[calc(100vw-2rem)] max-w-[1400px] flex-col gap-3 overflow-hidden p-0 sm:!max-w-[1400px]"
        aria-describedby={`${name}-terminal-description`}
      >
        <DialogHeader className="px-5 pt-5">
          <DialogTitle>Shell · {name}</DialogTitle>
          <DialogDescription id={`${name}-terminal-description`}>
            {"app" in target
              ? `Ephemeral app CLI as ${name}. Closing this dialog stops the container.`
              : `Interactive shell in the running ${name} service. Closing this dialog disconnects the shell.`}
          </DialogDescription>
        </DialogHeader>
        <div className="flex min-h-0 flex-1 bg-[#09090b] p-3">
          <div ref={setTerminalElement} className="min-h-0 min-w-0 flex-1 overflow-hidden" />
        </div>
        <DialogFooter className="items-center border-t px-5 py-3 sm:justify-between">
          <span className="text-xs text-muted-foreground" aria-live="polite">
            {connectionState === "connecting" && "Connecting…"}
            {connectionState === "connected" && "Connected"}
            {connectionState === "closed" && "Shell closed"}
            {connectionState === "error" && "Connection failed"}
          </span>
          <div className="flex gap-2">
            {(connectionState === "closed" || connectionState === "error") && (
              <Button variant="outline" onClick={() => setAttempt((value) => value + 1)}>
                Reconnect
              </Button>
            )}
            <Button onClick={onClose}>Close</Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
