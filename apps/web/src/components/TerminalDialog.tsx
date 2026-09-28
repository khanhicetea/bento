import { useEffect, useState, type ReactNode } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { api } from "../api/client.ts";
import { StateBadge } from "./DomainState.tsx";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";

type ConnectionState = "connecting" | "connected" | "reconnecting" | "detached" | "closed" | "error";

// Server close codes (see internal/api/terminal.go).
const closeTakenOver = 4001;
const closeIdle = 1008;
const maxRetries = 6;

// The shell outlives the socket for 15 minutes; remembering its id per tab
// lets a reload, a network blip, or switching tabs reattach to it.
const sessionKey = (path: string) => `bento.terminal.${path}`;
function loadSession(key: string) {
  try {
    return sessionStorage.getItem(key) ?? undefined;
  } catch {
    return undefined;
  }
}
function saveSession(key: string, id: string | null) {
  try {
    if (id) sessionStorage.setItem(key, id);
    else sessionStorage.removeItem(key);
  } catch {
    return;
  }
}

const stateBadge: Record<ConnectionState, string> = {
  connected: "healthy",
  connecting: "queued",
  reconnecting: "queued",
  detached: "stopped",
  closed: "stopped",
  error: "failed",
};
const stateLabel: Record<ConnectionState, string> = {
  connected: "connected",
  connecting: "connecting",
  reconnecting: "reconnecting",
  detached: "open in another tab",
  closed: "closed",
  error: "disconnected",
};

export function TerminalPanel({
  appId,
  mode,
  onModeChange,
}: {
  appId: string;
  mode: "tool" | "running";
  onModeChange?: (mode: "tool" | "running") => void;
}) {
  return (
    <TerminalView path={api.apps.terminalPath(appId, mode)}>
      {mode === "running" && (
        <span className="text-xs text-amber-600">Live app container — changes affect production</span>
      )}
      {onModeChange && (
        <div className="seg ml-auto">
          <button type="button" aria-pressed={mode === "tool"} onClick={() => onModeChange("tool")}>
            Tool shell
          </button>
          <button type="button" aria-pressed={mode === "running"} onClick={() => onModeChange("running")}>
            Live app
          </button>
        </div>
      )}
    </TerminalView>
  );
}

/**
 * Dialog onEscapeKeyDown handler: while the terminal has focus, Esc belongs
 * to the shell (vim, less, readline), so the dialog stays open. xterm ignores
 * defaultPrevented and still sends the key.
 */
export function keepEscapeInTerminal(event: KeyboardEvent) {
  if (event.target instanceof Element && event.target.closest(".terminal-host")) event.preventDefault();
}

/** A reattachable WebSocket shell at a terminal endpoint path. */
export function TerminalView({ path, children }: { path: string; children?: ReactNode }) {
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  const [state, setState] = useState<ConnectionState>("connecting");
  const [exitCode, setExitCode] = useState<number | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [socket, setSocket] = useState<WebSocket | null>(null);
  useEffect(() => {
    if (!element) return;
    const key = sessionKey(path);
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: '"SFMono-Regular", Consolas, monospace',
      fontSize: 14,
      scrollback: 5_000,
      theme: { background: "#16140f", foreground: "#ede6d6", cursor: "#e2603f", selectionBackground: "#3a352e" },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(element);
    fit.fit();
    setExitCode(null);
    const encoder = new TextEncoder();
    let current: WebSocket | null = null;
    let disposed = false;
    let retries = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let everConnected = false;

    const connect = () => {
      setState(everConnected ? "reconnecting" : "connecting");
      const ws = new WebSocket(api.terminalUrl(path, terminal.cols, terminal.rows, loadSession(key)));
      ws.binaryType = "arraybuffer";
      current = ws;
      setSocket(ws);
      let finished = false;
      ws.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
        if (typeof event.data !== "string") {
          terminal.write(new Uint8Array(event.data));
          return;
        }
        const message = JSON.parse(event.data) as { type: string; code?: number; id?: string; resumed?: boolean };
        if (message.type === "session" && message.id) {
          if (message.resumed) terminal.reset();
          else if (everConnected) terminal.write("\r\n\x1b[2m[previous shell ended; started a new one]\x1b[0m\r\n");
          saveSession(key, message.id);
          everConnected = true;
          retries = 0;
          setState("connected");
        } else if (message.type === "exit") {
          finished = true;
          saveSession(key, null);
          setExitCode(message.code ?? -1);
        }
      };
      ws.onclose = (event) => {
        if (disposed) return;
        if (finished) return setState("closed");
        if (event.code === closeTakenOver) return setState("detached");
        if (event.code === closeIdle || retries >= maxRetries) return setState("error");
        setState("reconnecting");
        retryTimer = setTimeout(connect, Math.min(1000 * 2 ** retries++, 15_000));
      };
    };
    connect();

    const input = terminal.onData((data) => {
      if (current?.readyState === WebSocket.OPEN) current.send(encoder.encode(data));
    });
    const resize = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        return;
      }
      if (current?.readyState === WebSocket.OPEN)
        current.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
    });
    resize.observe(element);
    terminal.focus();
    return () => {
      disposed = true;
      clearTimeout(retryTimer);
      resize.disconnect();
      input.dispose();
      current?.close();
      terminal.dispose();
    };
  }, [element, path, attempt]);
  const live = state === "connected";
  return (
    <div className="box">
      <div className="cell flex flex-wrap items-center gap-2 py-2.5!">
        <StateBadge state={stateBadge[state]} label={stateLabel[state]} />
        {exitCode !== null && <StateBadge state={exitCode === 0 ? "succeeded" : "failed"} label={`Exit ${exitCode}`} />}
        {(state === "closed" || state === "error" || state === "detached") && (
          <Button size="sm" variant="ghost" onClick={() => setAttempt((value) => value + 1)}>
            {state === "detached" ? "Take over" : state === "closed" ? "New shell" : "Reconnect"}
          </Button>
        )}
        {live && (
          <Button
            size="sm"
            variant="ghost"
            title="Stop the shell now instead of keeping it for 15 minutes after you leave"
            onClick={() => socket?.send(JSON.stringify({ type: "close" }))}
          >
            End session
          </Button>
        )}
        {children}
      </div>
      <div className="terminal-frame">
        <div ref={setElement} className="terminal-host" />
      </div>
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
      <DialogContent className="sm:max-w-5xl" onEscapeKeyDown={keepEscapeInTerminal}>
        <DialogHeader>
          <DialogTitle>{title} — terminal</DialogTitle>
          <DialogDescription>
            Runs as the app identity. Tool shells start no daemons and are kept 15 minutes after you disconnect; history
            persists in the app home. Esc goes to the shell; close with ×.
          </DialogDescription>
        </DialogHeader>
        <TerminalPanel appId={appId} mode={mode} />
      </DialogContent>
    </Dialog>
  );
}
