import { Database } from "bun:sqlite";
import { join } from "node:path";
import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { runCli } from "../../src/main.ts";
import { STATE_DATABASE_SCHEMA_VERSION } from "../../src/services/state_database.ts";

const entry = new URL("../../src/main.ts", import.meta.url).pathname;

bunRuntime.test("retired tui is absent from help and refused as an unknown command", () => {
  const help = Bun.spawnSync([process.execPath, entry, "--help"]);
  assertEquals(help.exitCode, 0);
  assertEquals(new TextDecoder().decode(help.stderr).includes("tui"), false);
  for (const name of ["serve", "migrate", "app"]) {
    assertEquals(new TextDecoder().decode(help.stderr).includes(name), true);
  }
  const retired = Bun.spawnSync([process.execPath, entry, "tui"]);
  assertEquals(retired.exitCode, 2);
  assertEquals(new TextDecoder().decode(retired.stderr).includes("Unknown argument: tui"), true);
});

bunRuntime.test("serve migrates and starts on loopback", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-serve-migrate-" });
  const child = Bun.spawn(
    [
      process.execPath,
      entry,
      "--stack",
      root,
      "--repo-root",
      bunRuntime.cwd(),
      "serve",
      "--port",
      "0",
    ],
    { stdout: "pipe", stderr: "pipe" },
  );
  try {
    const reader = child.stderr.getReader();
    let text = "";
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const url = await Promise.race([
      (async () => {
        while (!/http:\/\/127\.0\.0\.1:\d+/.test(text)) {
          const chunk = await reader.read();
          if (chunk.done) throw new Error(`serve exited before listening: ${text}`);
          text += new TextDecoder().decode(chunk.value);
        }
        return text.match(/http:\/\/127\.0\.0\.1:\d+/)![0];
      })(),
      new Promise<never>((_, reject) => {
        timeout = setTimeout(() => reject(new Error(`serve timeout: ${text}`)), 10000);
      }),
    ]).finally(() => clearTimeout(timeout));
    const response = await fetch(url);
    assertEquals(response.status, 200);
    using database = new Database(join(root, "state.db"), { readonly: true });
    const version = database
      .query<{ version: number }, []>("SELECT version FROM schema_migrations ORDER BY version DESC")
      .get();
    assertEquals(version?.version, STATE_DATABASE_SCHEMA_VERSION);
  } finally {
    child.kill();
    await child.exited;
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test(
  "app shell remains scriptable and prune retains its direct exact prompt",
  async () => {
    const root = await bunRuntime.makeTempDir({ prefix: "bento-cli-prompt-" });
    const base = ["--stack", root, "--repo-root", bunRuntime.cwd()];
    const originalPrompt = globalThis.prompt;
    try {
      assertEquals(await runCli([...base, "init"]), 0);
      assertEquals(
        await runCli([
          ...base,
          "app",
          "create",
          "demo",
          "--domain",
          "demo.test",
          "--database-engine",
          "sqlite",
          "--no-apply",
        ]),
        0,
      );
      assertEquals(await runCli([...base, "app", "shell", "demo", "--print"]), 0);
      assertEquals(
        await runCli([...base, "app", "delete", "demo", "--confirm", "delete demo", "--no-apply"]),
        0,
      );
      let promptText = "";
      globalThis.prompt = (message) => {
        promptText = message ?? "";
        return "DELETE";
      };
      assertEquals(await runCli([...base, "app", "prune", "demo"]), 10);
      assertEquals(promptText.includes("Type 'delete'"), true);
      assertEquals((await bunRuntime.stat(join(root, "homes", "demo"))).isDirectory, true);
    } finally {
      globalThis.prompt = originalPrompt;
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);
