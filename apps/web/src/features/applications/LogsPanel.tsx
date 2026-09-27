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
    <div className="box">
      <div className="cell flex flex-wrap items-center gap-2 py-2.5!">
        <span
          className={`dot ${status === "streaming" ? "" : status === "connecting" || status === "reconnecting" ? "dot--wait" : "dot--bad"}`}
        />
        <span className="note">
          {status} · {lines.length}
        </span>
        <Input
          className="h-9 min-w-40 flex-1 rounded-full"
          placeholder="Filter"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        />
        <div className="seg">
          <button
            type="button"
            aria-pressed={follow}
            onClick={() => {
              setFollow((value) => !value);
              setAtBottom(true);
            }}
          >
            Follow
          </button>
          <button type="button" aria-pressed={wrap} aria-label="Wrap lines" onClick={() => setWrap((value) => !value)}>
            <WrapText className="size-4" />
          </button>
        </div>
        <button
          type="button"
          className="icon-btn"
          aria-label="Copy visible logs"
          onClick={() =>
            void navigator.clipboard.writeText(visible.map((line) => `${line.ts} ${line.line}`).join("\n"))
          }
        >
          <Clipboard />
        </button>
        <button type="button" className="icon-btn" aria-label="Clear logs" onClick={() => setLines([])}>
          <Trash2 />
        </button>
      </div>
      <pre
        ref={viewport}
        onScroll={(event) => {
          const node = event.currentTarget;
          setAtBottom(node.scrollHeight - node.scrollTop - node.clientHeight < 40);
        }}
        className={`console ${wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre"}`}
      >
        {visible.map((line, index) => (
          <div key={`${line.ts}-${index}`}>
            <span>{line.ts.slice(11, 19)} </span>
            {line.line}
          </div>
        ))}
      </pre>
      {follow && !atBottom && (
        <Button
          className="fixed right-6 bottom-24 rounded-full shadow-lg md:bottom-8"
          onClick={() => {
            const node = viewport.current;
            if (node) node.scrollTop = node.scrollHeight;
            setAtBottom(true);
          }}
        >
          Latest ↓
        </Button>
      )}
    </div>
  );
}
