import { test, expect } from "bun:test";

const moduleUrl = new URL("../../src/ui/colors.ts", import.meta.url).href;

function runColors(env: Record<string, string>): string {
  const result = Bun.spawnSync(
    [
      process.execPath,
      "-e",
      `import { colors } from ${JSON.stringify(moduleUrl)}; console.log(colors.bold(colors.cyan("heading")));`,
    ],
    {
      env: { ...process.env, CI: "", FORCE_COLOR: "", NO_COLOR: "", ...env },
      stdout: "pipe",
      stderr: "pipe",
    },
  );
  expect(result.exitCode).toBe(0);
  return result.stdout.toString().trim();
}

test("CLI colors default to plain text and respect NO_COLOR", () => {
  expect(runColors({})).toBe("heading");
  expect(runColors({ FORCE_COLOR: "1", NO_COLOR: "1" })).toBe("heading");
});

test("CLI colors use Bun ANSI codes and reset nested styles", () => {
  const result = runColors({ FORCE_COLOR: "1" });
  expect(result).toMatch(/\x1b\[(?:3[6]|9[6]|38;5;\d+)mheading\x1b\[39m/);
  expect(result).toStartWith("\x1b[1m");
  expect(result).toEndWith("\x1b[22m");
  expect(result.replace(/\x1b\[[\d;]*m/g, "")).toBe("heading");
});
