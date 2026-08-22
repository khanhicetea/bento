import { describe, expect, test } from "bun:test";
import { assertWebCommand, commandCatalog } from "../../src/server/router.ts";

describe("web command boundary", () => {
  test("catalog covers every TUI management area", () => {
    const categories = commandCatalog.map((group) => group.category);
    expect(categories).toEqual([
      "Stack",
      "Applications",
      "Databases",
      "PHP",
      "Routing & TLS",
      "Jobs",
      "Operations",
      "Templates",
    ]);
    const commands = commandCatalog.flatMap((group) => group.commands);
    for (const prefix of [
      "app ",
      "proxy ",
      "mysql ",
      "postgres ",
      "sqlite ",
      "php ",
      "cron ",
      "worker ",
      "logs ",
      "template ",
      "deploy ",
    ]) {
      expect(commands.some((command) => command.startsWith(prefix))).toBe(true);
    }
  });

  test("accepts argv only and rejects interactive browser actions", () => {
    expect(() => assertWebCommand(["status"])).not.toThrow();
    expect(() => assertWebCommand(["app", "shell", "demo", "--print"])).not.toThrow();
    expect(() => assertWebCommand(["template", "select", "--source", "/tmp/vhost"])).not.toThrow();
    expect(() => assertWebCommand(["app", "shell", "demo"])).toThrow();
    expect(() => assertWebCommand(["logs", "access", "report", "--attach"])).toThrow();
    expect(() => assertWebCommand(["unknown", "anything"])).toThrow();
    expect(() => assertWebCommand(["status\nserve"])).toThrow();
  });
});
