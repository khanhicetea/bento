/**
 * Phase D — distribution parity (F-28 / F-29 / F-30).
 *
 * - Source mode always exercises asset digest + digest-addressed materialize cache.
 * - When BENTO_BIN (or dist/bento) is available, smoke-test the compiled artifact and
 *   compare generated managed files against source mode for identical inputs.
 *
 * Set REQUIRE_BENTO_BIN=1 to fail when no binary is present (used by `bun run test:parity`).
 */

import { runtime as bunRuntime, assertEquals, assertNotEquals } from "../runtime.ts";
import { join, resolve } from "node:path";
import { runCli } from "../../src/main.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { stateToJson } from "../../src/schemas/state.ts";
import {
  isParityManagedPath,
  materializeDockerAssets,
  normalizeParityText,
} from "../../src/services/assets_materialize.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { BENTO_VERSION, BUN_TARGET_VERSION, versionBanner } from "../../src/version.ts";

async function loadState(stack: string) {
  return await new StateStore(createPlatform(stack, bunRuntime.cwd())).load();
}

async function withStack(fn: (stack: string) => Promise<void>) {
  const stack = await bunRuntime.makeTempDir({ prefix: "bento-parity-" });
  try {
    await fn(stack);
  } finally {
    await bunRuntime.remove(stack, { recursive: true });
  }
}

async function resolveBentoBin(): Promise<string | null> {
  const envBin = bunRuntime.env.get("BENTO_BIN");
  if (envBin && envBin.length > 0) {
    try {
      const st = await bunRuntime.stat(envBin);
      if (st.isFile) return resolve(envBin);
    } catch {
      // fall through
    }
  }
  const dist = resolve("dist/bento");
  try {
    const st = await bunRuntime.stat(dist);
    if (st.isFile) return dist;
  } catch {
    // missing
  }
  return null;
}

async function runBin(
  bin: string,
  args: string[],
  opts?: { cwd?: string; env?: Record<string, string> },
): Promise<{ code: number; stdout: string; stderr: string }> {
  const cmd = new bunRuntime.Command(bin, {
    args,
    cwd: opts?.cwd,
    env: opts?.env,
    stdout: "piped",
    stderr: "piped",
  });
  const out = await cmd.output();
  return {
    code: out.code,
    stdout: new TextDecoder().decode(out.stdout),
    stderr: new TextDecoder().decode(out.stderr),
  };
}

bunRuntime.test("compiled CLI also rejects the retired tui command", async () => {
  const bin = await resolveBentoBin();
  if (!bin) {
    assertEquals(bunRuntime.env.get("REQUIRE_BENTO_BIN"), undefined);
    return;
  }
  const help = await runBin(bin, ["--help"]);
  assertEquals(help.code, 0);
  assertEquals(help.stderr.includes("tui"), false);
  const retired = await runBin(bin, ["tui"]);
  assertEquals(retired.code, 2);
  assertEquals(retired.stderr.includes("Unknown argument: tui"), true);
});

async function collectFiles(root: string, base = ""): Promise<Map<string, string>> {
  const map = new Map<string, string>();
  async function walk(dir: string, rel: string) {
    let names: string[];
    try {
      names = [];
      for await (const e of bunRuntime.readDir(dir)) names.push(e.name);
    } catch {
      return;
    }
    names.sort();
    for (const name of names) {
      const full = join(dir, name);
      const childRel = rel ? `${rel}/${name}` : name;
      const st = await bunRuntime.lstat(full);
      if (st.isDirectory) {
        // Skip volatile transaction dirs; walk other trees (including .asset-cache filter below).
        if (name === ".staging" || name === ".transaction-backup") continue;
        await walk(full, childRel);
      } else if (st.isFile) {
        if (!isParityManagedPath(childRel)) continue;
        // skip cache-only and cert material that is intentionally non-deterministic
        if (childRel.startsWith(".asset-cache/")) continue;
        if (childRel.startsWith("certs/")) continue;
        if (childRel.endsWith(".materialized.json")) continue;
        if (childRel.endsWith(".generation.json")) continue;
        const bytes = await bunRuntime.readFile(full);
        // text compare for config; binary would be hex
        try {
          map.set(childRel, normalizeParityText(new TextDecoder().decode(bytes)));
        } catch {
          map.set(childRel, `binary:${bytes.length}`);
        }
      }
    }
  }
  await walk(root, base);
  return map;
}

bunRuntime.test("version banner reports bento and pinned Bun target", () => {
  const banner = versionBanner();
  assertEquals(banner.includes(BENTO_VERSION), true);
  assertEquals(banner.includes(BUN_TARGET_VERSION), true);
  assertEquals(banner, `bento ${BENTO_VERSION} (bun ${BUN_TARGET_VERSION})`);
});

bunRuntime.test("asset digest is stable across resolver instances", async () => {
  const a = createPlatform(await bunRuntime.makeTempDir(), bunRuntime.cwd());
  const b = createPlatform(await bunRuntime.makeTempDir(), bunRuntime.cwd());
  const da = await a.assets.digest();
  const db = await b.assets.digest();
  assertEquals(da, db);
  assertEquals(da.length, 64);
});

bunRuntime.test("materialize uses digest-addressed cache and skips republish", async () => {
  await withStack(async (stack) => {
    const platform = createPlatform(stack, bunRuntime.cwd());
    const first = await materializeDockerAssets(platform, ["8.5"]);
    assertEquals(first.digest.length, 64);
    assertEquals(first.published, true);
    assertEquals(await bunRuntime.stat(join(first.cacheDir, ".ready")).then(() => true), true);
    assertEquals(
      await bunRuntime.stat(join(stack, "docker/nginx/Dockerfile")).then(() => true),
      true,
    );
    assertEquals(await bunRuntime.stat(join(stack, "helpers/bento.php")).then(() => true), true);
    const drainPhp = await bunRuntime.readTextFile(join(stack, "helpers/deploy-drain.php"));
    const drainSh = await bunRuntime.readTextFile(join(stack, "helpers/deploy-drain.sh"));
    assertEquals(drainPhp.includes("resetOpcache"), true);
    assertEquals(drainPhp.includes("$previousUmask = umask(0022);"), true);
    assertEquals(drainPhp.includes("umask($previousUmask);"), true);
    assertEquals(drainSh.includes("deploy-drain.php"), true);
    assertEquals(drainSh.includes("bento deploy drain"), false);
    assertEquals(
      await bunRuntime.stat(join(stack, "docker/php/helpers/deploy-drain.php")).then(() => true),
      true,
    );

    const meta1 = await bunRuntime.readTextFile(join(stack, "docker/.materialized.json"));
    assertEquals(JSON.parse(meta1).digest, first.digest);
    assertEquals(JSON.parse(meta1).cacheDir, `.asset-cache/${first.digest}`);

    const second = await materializeDockerAssets(platform, ["8.5"]);
    assertEquals(second.digest, first.digest);
    assertEquals(second.cacheDir, first.cacheDir);
    assertEquals(second.published, false);

    // Cache entry remains the single source of truth
    const cacheDocker = join(first.cacheDir, "docker/nginx/Dockerfile");
    const pubDocker = join(stack, "docker/nginx/Dockerfile");
    assertEquals(
      await bunRuntime.readTextFile(cacheDocker),
      await bunRuntime.readTextFile(pubDocker),
    );
  });
});

bunRuntime.test("source init/render/status smoke (F-28)", async () => {
  await withStack(async (stack) => {
    const base = ["--stack", stack, "--repo-root", bunRuntime.cwd()];
    assertEquals(await runCli([...base, "init"]), 0);
    assertEquals(await runCli([...base, "render"]), 0);
    assertEquals(await runCli([...base, "status"]), 0);
    assertEquals(await runCli([...base, "version"]), 0);

    // digest-addressed cache present after render
    const meta = JSON.parse(
      await bunRuntime.readTextFile(join(stack, "docker/.materialized.json")),
    );
    assertEquals(typeof meta.digest, "string");
    assertEquals(
      await bunRuntime.stat(join(stack, ".asset-cache", meta.digest, ".ready")).then(() => true),
      true,
    );
  });
});

bunRuntime.test({
  name: "compiled binary smoke + source/compiled parity (F-29 / F-30)",
  sanitizeResources: false,
  sanitizeOps: false,
  fn: async () => {
    const bin = await resolveBentoBin();
    if (!bin) {
      if (bunRuntime.env.get("REQUIRE_BENTO_BIN") === "1") {
        throw new Error(
          "BENTO_BIN not set and dist/bento missing; run `bun run compile` or `bun run test:parity`",
        );
      }
      console.log(
        "skip compiled parity: no BENTO_BIN / dist/bento (set REQUIRE_BENTO_BIN=1 to require)",
      );
      return;
    }

    // F-29: version / init / render / status without needing Bun on PATH
    {
      const ver = await runBin(bin, ["version"], {
        cwd: "/tmp",
        env: {
          PATH: "/usr/bin:/bin",
          HOME: bunRuntime.env.get("HOME") ?? "/tmp",
        },
      });
      assertEquals(ver.code, 0, ver.stderr);
      assertEquals(ver.stdout.includes(BENTO_VERSION), true);
      assertEquals(ver.stdout.includes(BUN_TARGET_VERSION), true);
    }

    // PostgreSQL command smoke in the freshly compiled distribution. A plain
    // `bun run test` may discover an older optional dist/bento, so only run
    // new-command assertions when the parity task explicitly requires it.
    if (bunRuntime.env.get("REQUIRE_BENTO_BIN") === "1") {
      await withStack(async (stack) => {
        const env = {
          PATH: "/usr/bin:/bin",
          HOME: bunRuntime.env.get("HOME") ?? "/tmp",
        };
        assertEquals((await runBin(bin, ["--stack", stack, "init"], { cwd: "/tmp", env })).code, 0);
        assertEquals(
          (
            await runBin(bin, ["--stack", stack, "postgres", "add", "17", "--no-apply"], {
              cwd: "/tmp",
              env,
            })
          ).code,
          0,
        );
        const list = await runBin(bin, ["--stack", stack, "postgres", "list"], {
          cwd: "/tmp",
          env,
        });
        assertEquals(list.code, 0, list.stderr);
        assertEquals(list.stdout.includes("postgres17"), true);
        assertEquals(
          (
            await runBin(
              bin,
              [
                "--stack",
                stack,
                "app",
                "create",
                "pgparity",
                "--domain",
                "pgparity.test",
                "--database-engine",
                "postgres",
                "--postgres",
                "17",
                "--no-apply",
              ],
              { cwd: "/tmp", env },
            )
          ).code,
          0,
        );
        const shellPlan = await runBin(
          bin,
          ["--stack", stack, "postgres", "shell", "--app", "pgparity", "--print"],
          { cwd: "/tmp", env },
        );
        assertEquals(shellPlan.code, 0, shellPlan.stderr);
        assertEquals(shellPlan.stdout.includes("psql"), true);
        const state = await loadState(stack);
        const postgresBinding = state.apps.pgparity!.databases.find(
          (binding) => binding.engine === "postgres",
        );
        assertEquals(
          shellPlan.stdout.includes(
            postgresBinding?.engine === "postgres" ? postgresBinding.password : "",
          ),
          false,
        );
        assertEquals(
          (
            await runBin(bin, ["--stack", stack, "postgres", "remove", "17"], {
              cwd: "/tmp",
              env,
            })
          ).code,
          10,
        );
      });
    }

    await withStack(async (baseStack) => {
      // Shared inputs: init once via source, clone to two stacks
      const seed = ["--stack", baseStack, "--repo-root", bunRuntime.cwd()];
      assertEquals(await runCli([...seed, "init"]), 0);
      assertEquals(await runCli([...seed, "postgres", "add", "17", "--no-apply"]), 0);
      // Create an app so generated surface is non-trivial
      assertEquals(
        await runCli([
          ...seed,
          "app",
          "create",
          "parity",
          "--domain",
          "parity.test",
          "--database-engine",
          "postgres",
          "--postgres",
          "postgres17",
          "--no-apply",
        ]),
        0,
      );
      assertEquals(
        await runCli([
          ...seed,
          "app",
          "create",
          "process-parity",
          "--domain",
          "process-parity.test",
          "--runtime",
          "node",
          "--runtime-version",
          "24",
          "--start",
          "node",
          "--start",
          "server.js",
          "--no-apply",
        ]),
        0,
      );

      const srcStack = await bunRuntime.makeTempDir({
        prefix: "bento-parity-src-",
      });
      const binStack = await bunRuntime.makeTempDir({
        prefix: "bento-parity-bin-",
      });
      try {
        await copyDir(baseStack, srcStack);
        await copyDir(baseStack, binStack);
        // Drop any generated output so both modes re-render cleanly
        for (const s of [srcStack, binStack]) {
          await bunRuntime.remove(join(s, "generated"), { recursive: true }).catch(() => {});
          await bunRuntime.remove(join(s, "docker"), { recursive: true }).catch(() => {});
          await bunRuntime.remove(join(s, "helpers"), { recursive: true }).catch(() => {});
          await bunRuntime.remove(join(s, ".asset-cache"), { recursive: true }).catch(() => {});
        }

        const srcCode = await runCli([
          "--stack",
          srcStack,
          "--repo-root",
          bunRuntime.cwd(),
          "render",
        ]);
        const binRender = await runBin(bin, ["--stack", binStack, "render"], {
          cwd: "/tmp",
          env: {
            PATH: "/usr/bin:/bin",
            HOME: bunRuntime.env.get("HOME") ?? "/tmp",
          },
        });
        assertEquals(srcCode, 0);
        assertEquals(binRender.code, 0, binRender.stderr + binRender.stdout);

        const srcStatus = await runCli([
          "--stack",
          srcStack,
          "--repo-root",
          bunRuntime.cwd(),
          "status",
        ]);
        const binStatus = await runBin(bin, ["--stack", binStack, "status"], {
          cwd: "/tmp",
          env: {
            PATH: "/usr/bin:/bin",
            HOME: bunRuntime.env.get("HOME") ?? "/tmp",
          },
        });
        assertEquals(srcStatus, 0);
        assertEquals(binStatus.code, 0, binStatus.stderr);

        // State transitions equal
        const srcState = stateToJson(await loadState(srcStack));
        const binState = stateToJson(await loadState(binStack));
        assertEquals(normalizeParityText(srcState), normalizeParityText(binState));

        // Asset digests equal
        const srcMeta = JSON.parse(
          await bunRuntime.readTextFile(join(srcStack, "docker/.materialized.json")),
        );
        const binMeta = JSON.parse(
          await bunRuntime.readTextFile(join(binStack, "docker/.materialized.json")),
        );
        assertEquals(srcMeta.digest, binMeta.digest);

        // Generated managed files byte-equivalent (normalized)
        const srcFiles = await collectFiles(join(srcStack, "generated"));
        const binFiles = await collectFiles(join(binStack, "generated"));
        assertEquals([...srcFiles.keys()].sort(), [...binFiles.keys()].sort());
        for (const [rel, content] of srcFiles) {
          assertEquals(binFiles.get(rel), content, `mismatch in generated/${rel}`);
        }

        // Docker + helpers published assets equal
        const srcDocker = await collectFiles(join(srcStack, "docker"));
        const binDocker = await collectFiles(join(binStack, "docker"));
        assertEquals([...srcDocker.keys()].sort(), [...binDocker.keys()].sort());
        for (const [rel, content] of srcDocker) {
          assertEquals(binDocker.get(rel), content, `mismatch in docker/${rel}`);
        }
        const srcHelpers = await collectFiles(join(srcStack, "helpers"));
        const binHelpers = await collectFiles(join(binStack, "helpers"));
        assertEquals([...srcHelpers.keys()].sort(), [...binHelpers.keys()].sort());
        for (const [rel, content] of srcHelpers) {
          assertEquals(binHelpers.get(rel), content, `mismatch in helpers/${rel}`);
        }

        // Normalized status diagnostics: same structure (strip stack path)
        // (status is printed by runCli to the process stdout; we re-run via bin capture)
        assertNotEquals(binStatus.stdout.length, 0);
        assertEquals(binStatus.stdout.includes("parity"), true);
        assertEquals(binStatus.stdout.includes(binStack), true);
      } finally {
        await bunRuntime.remove(srcStack, { recursive: true }).catch(() => {});
        await bunRuntime.remove(binStack, { recursive: true }).catch(() => {});
      }
    });
  },
});

async function copyDir(from: string, to: string) {
  await bunRuntime.mkdir(to, { recursive: true });
  for await (const entry of bunRuntime.readDir(from)) {
    const src = join(from, entry.name);
    const dest = join(to, entry.name);
    if (entry.isDirectory) {
      await copyDir(src, dest);
    } else if (entry.isFile) {
      await bunRuntime.copyFile(src, dest);
    }
  }
}
