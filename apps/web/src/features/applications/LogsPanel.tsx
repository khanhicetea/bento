import { useEffect, useRef, useState } from "react";
import { Clipboard, Trash2, WrapText } from "lucide-react";
import { api } from "../../api/client.ts";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

type Line = { ts: string; line: string };
const maxLines = 2000;

export function LogsPanel({ appId }: { appId: string }) {
  const [lines, setLines] = useState<Line[]>([]);
  const [follow, setFollow] = useState(true);
  const [atBottom, setAtBottom] = useState(true);
  const [wrap, setWrap] = useState(false);
  const [filter, setFilter] = useState("");
  const [status, setStatus] = useState("connecting");
  const viewport = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const source = new EventSource(api.apps.logsUrl(appId, lines.length === 0 ? 300 : 0, follow));
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
    // Existing lines deliberately survive follow reconnects.
  }, [appId, follow]);
  useEffect(() => {
    if (follow && atBottom) {
      const node = viewport.current;
      if (node) node.scrollTop = node.scrollHeight;
    }
  }, [lines.length, follow, atBottom]);
  const visible = filter ? lines.filter((line) => line.line.toLowerCase().includes(filter.toLowerCase())) : lines;
  return (
    <div className="grid gap-2">
      <div className="sticky top-16 z-10 flex flex-wrap items-center gap-2 rounded-lg border bg-background p-2 text-xs">
        <span className="text-muted-foreground">
          {status} · {lines.length} lines
        </span>
        <Input
          className="h-8 min-w-40 flex-1"
          placeholder="Filter logs"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        />
        <Button
          size="sm"
          variant={follow ? "default" : "outline"}
          onClick={() => {
            setFollow((value) => !value);
            setAtBottom(true);
          }}
        >
          {follow ? "Following" : "Follow"}
        </Button>
        <Button
          size="icon-sm"
          variant={wrap ? "default" : "outline"}
          aria-label="Toggle line wrapping"
          onClick={() => setWrap((value) => !value)}
        >
          <WrapText />
        </Button>
        <Button
          size="icon-sm"
          variant="outline"
          aria-label="Copy visible logs"
          onClick={() =>
            void navigator.clipboard.writeText(visible.map((line) => `${line.ts} ${line.line}`).join("\n"))
          }
        >
          <Clipboard />
        </Button>
        <Button size="icon-sm" variant="outline" aria-label="Clear logs" onClick={() => setLines([])}>
          <Trash2 />
        </Button>
      </div>
      <pre
        ref={viewport}
        onScroll={(event) => {
          const node = event.currentTarget;
          setAtBottom(node.scrollHeight - node.scrollTop - node.clientHeight < 40);
        }}
        className={`m-0 h-[calc(100vh-15rem)] min-h-96 overflow-auto rounded-lg bg-zinc-950 p-3 text-xs text-zinc-100 ${wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre"}`}
      >
        {visible.map((line, index) => (
          <div key={`${line.ts}-${index}`}>
            <span className="text-zinc-500">{line.ts.slice(11, 19)} </span>
            {line.line}
          </div>
        ))}
      </pre>
      {follow && !atBottom && (
        <Button
          className="fixed right-8 bottom-8"
          onClick={() => {
            const node = viewport.current;
            if (node) node.scrollTop = node.scrollHeight;
            setAtBottom(true);
          }}
        >
          Jump to latest
        </Button>
      )}
    </div>
  );
}
