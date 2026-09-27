import { useEffect, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { api } from "../api/client.ts";
import { StateBadge } from "./DomainState.tsx";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

type ConnectionState = "connecting" | "connected" | "closed" | "error";

export function TerminalPanel({
  appId,
  mode,
  onModeChange,
}: {
  appId: string;
  mode: "tool" | "running";
  onModeChange?: (mode: "tool" | "running") => void;
}) {
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  const [state, setState] = useState<ConnectionState>("connecting");
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!element) return;
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: '"SFMono-Regular", Consolas, monospace',
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
      } else terminal.write(new Uint8Array(event.data));
    };
    socket.onclose = () => setState((value) => (value === "error" ? value : "closed"));
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
      if (socket.readyState === WebSocket.OPEN)
        socket.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
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
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge
          state={
            state === "connected"
              ? "running"
              : state === "connecting"
                ? "queued"
                : state === "error"
                  ? "failed"
                  : "stopped"
          }
          label={state}
        />
        {exitCode !== null && <StateBadge state={exitCode === 0 ? "succeeded" : "failed"} label={`Exit ${exitCode}`} />}
        {onModeChange && (
          <>
            <Button size="sm" variant={mode === "tool" ? "default" : "outline"} onClick={() => onModeChange("tool")}>
              Tool shell
            </Button>
            <Button
              size="sm"
              variant={mode === "running" ? "default" : "outline"}
              onClick={() => onModeChange("running")}
            >
              Running instance
            </Button>
          </>
        )}
        {(state === "closed" || state === "error") && (
          <Button size="sm" variant="outline" onClick={() => setAttempt((value) => value + 1)}>
            Reconnect
          </Button>
        )}
      </div>
      <div ref={setElement} className="h-[calc(100vh-18rem)] min-h-96 overflow-hidden rounded-lg bg-zinc-950 p-2" />
    </div>
  );
}

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
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-5xl">
        <DialogHeader>
          <DialogTitle>{title} — terminal</DialogTitle>
          <DialogDescription>
            Runs as the app identity. Tool shells are ephemeral and start no daemons.
          </DialogDescription>
        </DialogHeader>
        <TerminalPanel appId={appId} mode={mode} />
      </DialogContent>
    </Dialog>
  );
}
