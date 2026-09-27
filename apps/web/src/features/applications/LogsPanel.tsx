import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client.ts";
import { Button } from "@/components/ui/button";

type Line = { ts: string; line: string };
const maxLines = 2000;

/**
 * Streams instance logs over SSE. The browser reconnects automatically and
 * resumes from the last event id (timestamp); lines are bounded client-side.
 */
export function LogsPanel({ appId }: { appId: string }) {
  const [lines, setLines] = useState<Line[]>([]);
  const [follow, setFollow] = useState(true);
  const [status, setStatus] = useState("connecting");
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setLines([]);
    const source = new EventSource(api.apps.logsUrl(appId, 300, follow));
    source.addEventListener("open", () => setStatus("streaming"));
    source.addEventListener("log", (event) => {
      const data = JSON.parse((event as MessageEvent<string>).data) as Line;
      setLines((current) => [...current, data].slice(-maxLines));
    });
    source.addEventListener("end", () => {
      setStatus("ended");
      if (!follow) source.close();
    });
    source.addEventListener("error", () => setStatus(follow ? "reconnecting" : "closed"));
    return () => source.close();
  }, [appId, follow]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" });
  }, [lines.length]);

  return (
    <div className="grid gap-2">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>{status}</span>
        <Button size="xs" variant="outline" onClick={() => setFollow((f) => !f)}>
          {follow ? "Stop following" : "Follow"}
        </Button>
      </div>
      <pre className="m-0 h-[55vh] overflow-auto rounded-lg bg-zinc-950 p-3 text-xs text-zinc-100">
        {lines.map((l, i) => (
          <div key={`${l.ts}-${i}`}>
            <span className="text-zinc-500">{l.ts.slice(11, 19)} </span>
            {l.line}
          </div>
        ))}
        <div ref={bottom} />
      </pre>
    </div>
  );
}
