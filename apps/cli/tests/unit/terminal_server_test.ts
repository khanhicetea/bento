import { describe, expect, test } from "bun:test";
import {
  closeTerminalSession,
  isSameOriginRequest,
  parseTerminalClientMessage,
  startTerminalSession,
  type TerminalSession,
} from "../../src/server/terminal.ts";

describe("web terminal boundary", () => {
  test("accepts only same-origin terminal mutations", () => {
    expect(
      isSameOriginRequest(
        new Request("http://127.0.0.1:8080/api/terminal?app=demo", {
          headers: { origin: "http://127.0.0.1:8080" },
        }),
      ),
    ).toBe(true);
    expect(
      isSameOriginRequest(
        new Request("http://127.0.0.1:8080/api/terminal", {
          headers: { origin: "https://attacker.example" },
        }),
      ),
    ).toBe(false);
    expect(isSameOriginRequest(new Request("http://127.0.0.1:8080/api/terminal"))).toBe(false);
  });

  test("streams PTY output and closes the transport with the session", async () => {
    const chunks: Uint8Array[] = [];
    let outputClosed = false;
    const session: TerminalSession = {
      command: ["bash", "-lc", "printf terminal-ready"],
      cwd: process.cwd(),
      app: "demo",
      closed: false,
      cleanup: async () => undefined,
    };

    await new Promise<void>((resolve, reject) => {
      startTerminalSession(session, {
        data: (data) => chunks.push(data.slice()),
        exit: () => resolve(),
        error: () => reject(new Error("terminal failed to start")),
        close: () => {
          outputClosed = true;
        },
      });
    });
    expect(new TextDecoder().decode(Buffer.concat(chunks))).toContain("terminal-ready");
    await closeTerminalSession(session);
    expect(outputClosed).toBe(true);
  });

  test("validates terminal input and bounded resize messages", () => {
    expect(parseTerminalClientMessage('{"type":"input","data":"ls\\r"}')).toEqual({
      type: "input",
      data: "ls\r",
    });
    expect(parseTerminalClientMessage('{"type":"resize","cols":120,"rows":40}')).toEqual({
      type: "resize",
      cols: 120,
      rows: 40,
    });
    expect(parseTerminalClientMessage('{"type":"resize","cols":0,"rows":40}')).toBeNull();
    expect(parseTerminalClientMessage('{"type":"input","data":42}')).toBeNull();
    expect(parseTerminalClientMessage("not-json")).toBeNull();
  });
});
