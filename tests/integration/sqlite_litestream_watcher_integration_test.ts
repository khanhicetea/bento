import { assertEquals } from "@std/assert";
import { join } from "@std/path";
import { isDockerAvailable, skipIf } from "./helpers.ts";

const IMAGE = "litestream/litestream:0.5.15";

Deno.test("Litestream watcher discovers private app databases and preserves WAL ownership", async () => {
  if (skipIf(!(await isDockerAvailable()), "Docker unavailable")) return;
  if (skipIf(Deno.uid() !== 0, "test requires root to model distinct app UIDs")) return;
  if (skipIf(!(await commandExists("python3")), "python3 unavailable for SQLite fixture")) return;
  if (skipIf(!(await commandExists("setpriv")), "setpriv unavailable for app UID fixture")) return;

  const root = await Deno.makeTempDir({ prefix: "bento-litestream-watcher-" });
  const name = `bento-litestream-watcher-${crypto.randomUUID().slice(0, 8)}`;
  const data = join(root, "data");
  const meta = join(root, "meta");
  const replica = join(root, "replica");
  const runtime = join(root, "run");
  const appA = join(data, "sqlite_a");
  const appB = join(data, "sqlite_b");
  const uidA = 12001;
  const uidB = 12002;

  try {
    await Deno.chmod(root, 0o755);
    for (const path of [data, meta, replica, runtime, appA, appB]) {
      await Deno.mkdir(path, { recursive: true, mode: 0o755 });
    }
    await Deno.chown(appA, uidA, uidA);
    await Deno.chown(appB, uidB, uidB);
    await Deno.chmod(appA, 0o700);
    await Deno.chmod(appB, 0o700);

    const configPath = join(root, "litestream.yml");
    await Deno.writeTextFile(
      configPath,
      `socket:
  enabled: true
  path: /run/litestream/control.sock
  permissions: 0600
dbs:
  - dir: /sqlite
    pattern: "database.sqlite"
    recursive: true
    watch: true
    meta-dir: /var/lib/litestream
    replica:
      url: file:///replica
      sync-interval: 1s
`,
    );
    const fixturePath = join(root, "fixture.py");
    await Deno.writeTextFile(
      fixturePath,
      `import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("pragma journal_mode=wal")
c.execute("create table if not exists t(x)")
c.execute("insert into t values (?)", (sys.argv[2],))
c.commit()
c.close()
`,
    );
    await Deno.chmod(fixturePath, 0o644);

    await startWatcher(name, { data, meta, replica, runtime, configPath });
    await runAs(uidA, fixturePath, join(appA, "database.sqlite"), "1");
    await runAs(uidB, fixturePath, join(appB, "database.sqlite"), "2");

    await waitFor(async () => {
      const out = await run([
        "docker",
        "exec",
        name,
        "litestream",
        "list",
        "-socket",
        "/run/litestream/control.sock",
        "-json",
      ]);
      if (out.code !== 0) return false;
      const parsed = JSON.parse(out.stdout) as { databases?: unknown[] };
      return parsed.databases?.length === 2;
    }, "two dynamically discovered databases");

    const sync = await run([
      "docker",
      "exec",
      name,
      "litestream",
      "sync",
      "-socket",
      "/run/litestream/control.sock",
      "-wait",
      "-timeout",
      "10",
      "/sqlite/sqlite_a/database.sqlite",
    ]);
    assertEquals(sync.code, 0, sync.stderr);
    await stopWatcher(name);

    for (const suffix of ["-wal", "-shm"]) {
      await Deno.remove(join(appA, `database.sqlite${suffix}`)).catch(() => {});
    }
    await startWatcher(name, { data, meta, replica, runtime, configPath });
    await waitFor(async () => {
      try {
        await Deno.stat(join(appA, "database.sqlite-wal"));
        await Deno.stat(join(appA, "database.sqlite-shm"));
        return true;
      } catch {
        return false;
      }
    }, "Litestream-recreated WAL and SHM files");

    assertEquals((await Deno.stat(join(appA, "database.sqlite-wal"))).uid, uidA);
    assertEquals((await Deno.stat(join(appA, "database.sqlite-shm"))).uid, uidA);
    await runAs(uidA, fixturePath, join(appA, "database.sqlite"), "3");
  } finally {
    await stopWatcher(name);
    await Deno.remove(root, { recursive: true }).catch(() => {});
  }
});

async function startWatcher(
  name: string,
  paths: { data: string; meta: string; replica: string; runtime: string; configPath: string },
): Promise<void> {
  const out = await run([
    "docker",
    "run",
    "-d",
    "--rm",
    "--name",
    name,
    "--user",
    "0:0",
    "--cap-drop",
    "ALL",
    "--cap-add",
    "DAC_OVERRIDE",
    "--cap-add",
    "CHOWN",
    "--cap-add",
    "FOWNER",
    "--read-only",
    "--security-opt",
    "no-new-privileges:true",
    "-v",
    `${paths.data}:/sqlite`,
    "-v",
    `${paths.meta}:/var/lib/litestream`,
    "-v",
    `${paths.replica}:/replica`,
    "-v",
    `${paths.runtime}:/run/litestream`,
    "-v",
    `${paths.configPath}:/etc/litestream/litestream.yml:ro`,
    IMAGE,
    "replicate",
    "-config",
    "/etc/litestream/litestream.yml",
  ]);
  assertEquals(out.code, 0, out.stderr);
}

async function stopWatcher(name: string): Promise<void> {
  await run(["docker", "stop", "-t", "10", name]);
}

async function runAs(uid: number, fixture: string, database: string, value: string): Promise<void> {
  const out = await run([
    "setpriv",
    `--reuid=${uid}`,
    `--regid=${uid}`,
    "--clear-groups",
    "python3",
    fixture,
    database,
    value,
  ]);
  assertEquals(out.code, 0, out.stderr);
}

async function waitFor(check: () => Promise<boolean>, description: string): Promise<void> {
  for (let attempt = 0; attempt < 50; attempt++) {
    if (await check()) return;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`timed out waiting for ${description}`);
}

async function commandExists(name: string): Promise<boolean> {
  return (await run(["sh", "-c", `command -v ${name}`])).code === 0;
}

async function run(args: string[]): Promise<{ code: number; stdout: string; stderr: string }> {
  try {
    const out = await new Deno.Command(args[0]!, {
      args: args.slice(1),
      stdout: "piped",
      stderr: "piped",
    }).output();
    return {
      code: out.code,
      stdout: new TextDecoder().decode(out.stdout),
      stderr: new TextDecoder().decode(out.stderr),
    };
  } catch (cause) {
    return { code: 1, stdout: "", stderr: cause instanceof Error ? cause.message : String(cause) };
  }
}
