import { useEffect, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { api } from "../api/client.ts";
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

/**
 * Interactive terminal over an authenticated WebSocket. Binary frames carry
 * terminal bytes; text frames carry JSON control (resize out, exit in).
 */
export function TerminalDialog({
  appId,
  title,
  mode,
  onClose,
}: {
  appId: string;
  title: string;
  mode: "tool" | "running";
  onClose: () => void;
}) {
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  const [state, setState] = useState<ConnectionState>("connecting");
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!element) return;
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: '"JetBrains Mono", "SFMono-Regular", Consolas, monospace',
      fontSize: 14,
      scrollback: 5_000,
      theme: { background: "#09090b", foreground: "#fafafa", cursor: "#fafafa", selectionBackground: "#3f3f46" },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(element);
    fit.fit();
    setState("connecting");
    setExitCode(null);
    const socket = new WebSocket(api.apps.terminalUrl(appId, mode, terminal.cols, terminal.rows));
    socket.binaryType = "arraybuffer";
    const encoder = new TextEncoder();
    socket.onopen = () => setState("connected");
    socket.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
      if (typeof event.data === "string") {
        const message = JSON.parse(event.data) as { type: string; code?: number };
        if (message.type === "exit") setExitCode(message.code ?? -1);
        return;
      }
      terminal.write(new Uint8Array(event.data));
    };
    socket.onclose = () => setState((s) => (s === "error" ? s : "closed"));
    socket.onerror = () => setState("error");
    const input = terminal.onData((data) => {
      if (socket.readyState === WebSocket.OPEN) socket.send(encoder.encode(data));
    });
    const resize = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        return;
      }
      if (socket.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
      }
    });
    resize.observe(element);
    terminal.focus();
    return () => {
      resize.disconnect();
      input.dispose();
      socket.close();
      terminal.dispose();
    };
  }, [element, appId, mode, attempt]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-5xl">
        <DialogHeader>
          <DialogTitle>
            {title} — {mode === "tool" ? "tooling shell" : "running instance"}
          </DialogTitle>
          <DialogDescription>
            Runs as the app identity.{" "}
            {mode === "tool" ? "A scoped ephemeral container; no daemons start." : "Exec into the running instance."}{" "}
            Status: {state}
            {exitCode !== null && ` · exited ${exitCode}`}
          </DialogDescription>
        </DialogHeader>
        <div ref={setElement} className="h-[60vh] overflow-hidden rounded-md bg-[#09090b] p-2" />
        <DialogFooter>
          {(state === "closed" || state === "error") && (
            <Button variant="outline" onClick={() => setAttempt((n) => n + 1)}>
              Reconnect
            </Button>
          )}
          <Button onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
