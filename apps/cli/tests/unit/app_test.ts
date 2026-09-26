import { runtime as bunRuntime, assertEquals, assertThrows } from "../runtime.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import {
  allocateIdentity,
  capacityWarnings,
  materializeAppHome,
  provisionApp,
} from "../../src/services/app.ts";
import { addPhpVersion, buildCliExec, cliRunComposeCommand } from "../../src/services/php.ts";
import { minicrondComposeCommand } from "../../src/services/minicrond.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import { parseDesiredState, stateToJson } from "../../src/schemas/state.ts";
import { enableDeploy } from "../../src/services/deploy.ts";
import {
  addMysqlVersion,
  buildMysqlShellPlan,
  createAppDatabase,
} from "../../src/services/mysql.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import type { Platform } from "../../src/platform/mod.ts";
import { join } from "node:path";

function testPlatform(root: string): Platform {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-16T12:00:00.000Z"),
    random: createSeededRandom("0123456789abcdef"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

bunRuntime.test("provisionApp creates distinct identities and domain ownership", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState("2026-07-16T12:00:00.000Z");
    const a = provisionApp(platform, state, {
      slug: "alpha",
      domain: "alpha.example",
      createDatabase: true,
    });
    state = a.state;
    const b = provisionApp(platform, state, {
      slug: "beta",
      domain: "beta.example",
    });
    state = b.state;

    assertEquals(a.app.uid !== b.app.uid, true);
    assertEquals(a.app.home, "/home/alpha");
    assertEquals(b.app.home, "/home/beta");
    assertEquals(state.domains["alpha.example"]?.kind, "app");
    assertEquals(state.domains["beta.example"]?.kind, "app");
    assertEquals(a.app.database.databases[0]?.name, "alpha");
    assertEquals(a.app.database.service, b.app.database.service); // same default service
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("PHP app provisioning reconciles its live scheduler service", () => {
  const platform = testPlatform("/tmp/bento-scheduler-reload-plan");
  const created = provisionApp(platform, createEmptyState(), {
    slug: "alpha",
    domain: "alpha.example",
  });
  assertEquals([...created.reloadPlan.phpRunner], ["php85-runner"]);
  assertEquals([...created.reloadPlan.phpFpm], ["php85"]);

  const updated = provisionApp(platform, created.state, {
    slug: "alpha",
    domain: "new.alpha.example",
    databaseEngine: "sqlite",
  });
  assertEquals([...updated.reloadPlan.phpRunner], ["php85-runner"]);
});

bunRuntime.test("provisionApp rejects duplicate domain links before state is built", () => {
  const platform = testPlatform("/tmp/bento-domain-duplicates");
  assertThrows(
    () =>
      provisionApp(platform, createEmptyState(), {
        slug: "alpha",
        domain: "alpha.example",
        aliases: ["alpha.example"],
      }),
    Error,
    "must not contain duplicates",
  );
});

bunRuntime.test(
  "materializeAppHome generates one stable SSH key pair with strict modes",
  async () => {
    const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
    try {
      const process = createRecordingProcessRunner(async (command) => {
        if (command[0] === "ssh-keygen" && command.includes("-f")) {
          const keyPath = command[command.indexOf("-f") + 1]!;
          await bunRuntime.writeTextFile(keyPath, "private-key\n");
          await bunRuntime.writeTextFile(
            `${keyPath}.pub`,
            "ssh-ed25519 public-key bento-app-alpha\n",
          );
        }
        return { code: 0, stdout: "", stderr: "" };
      });
      const platform = { ...testPlatform(root), process };
      const app = provisionApp(platform, createEmptyState(), {
        slug: "alpha",
        domain: "alpha.example",
      }).app;

      await materializeAppHome(platform, app);
      await materializeAppHome(platform, app, false);

      const sshDir = join(platform.paths.appHome(app.slug), ".ssh");
      const schedulerDir = join(platform.paths.appHome(app.slug), ".local/share/minicron");
      assertEquals((await platform.fs.stat(schedulerDir)).mode & 0o777, 0o700);
      assertEquals(
        (await platform.fs.stat(join(platform.paths.appHome(app.slug), ".local"))).mode & 0o777,
        0o700,
      );
      assertEquals((await platform.fs.stat(sshDir)).mode & 0o777, 0o700);
      assertEquals((await platform.fs.stat(join(sshDir, "id_ed25519"))).mode & 0o777, 0o600);
      assertEquals((await platform.fs.stat(join(sshDir, "id_ed25519.pub"))).mode & 0o777, 0o644);
      assertEquals(process.calls.filter((call) => call.command[0] === "ssh-keygen").length, 1);
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);

bunRuntime.test("app home refuses a symlinked scheduler data directory", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    const app = provisionApp(platform, createEmptyState(), {
      slug: "alpha",
      domain: "alpha.example",
    }).app;
    const home = platform.paths.appHome(app.slug);
    await bunRuntime.mkdir(join(home, ".local", "share"), { recursive: true });
    await bunRuntime.symlink(root, join(home, ".local", "share", "minicron"));
    let refused = false;
    try {
      await materializeAppHome(platform, app);
    } catch (error) {
      refused =
        error instanceof Error && error.message.includes("non-directory minicrond data path");
    }
    assertEquals(refused, true);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("domain collision is refused", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState();
    state = provisionApp(platform, state, {
      slug: "alpha",
      domain: "shared.example",
    }).state;
    assertThrows(
      () =>
        provisionApp(platform, state, {
          slug: "beta",
          domain: "shared.example",
        }),
      Error,
      "already owned",
    );
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("main domain change retains identity", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState();
    const first = provisionApp(platform, state, {
      slug: "alpha",
      domain: "old.example",
    });
    state = first.state;
    const second = provisionApp(platform, state, {
      slug: "alpha",
      domain: "new.example",
    });
    assertEquals(second.app.uid, first.app.uid);
    assertEquals(second.app.home, first.app.home);
    assertEquals(second.app.database.password, first.app.database.password);
    assertEquals(second.state.domains["old.example"], undefined);
    assertEquals(second.state.domains["new.example"]?.kind, "app");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("omitted php version preserves existing", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState();
    // add second php and assign app to it
    state = {
      ...state,
      phpVersions: [
        ...state.phpVersions,
        {
          version: "8.3" as never,
          service: "php83",
          image: "bento/php:8.3-fpm",
          processCap: 200,
        },
      ],
    };
    const first = provisionApp(platform, state, {
      slug: "alpha",
      domain: "a.example",
      phpVersion: "8.3",
    });
    state = first.state;
    // change defaults
    state = {
      ...state,
      defaults: { ...state.defaults, phpVersion: "8.5" as never },
    };
    const second = provisionApp(platform, state, {
      slug: "alpha",
      domain: "a.example",
    });
    assertEquals(second.app.phpVersion, "8.3");
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("database namespace enforced", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    const state = createEmptyState();
    assertThrows(
      () =>
        provisionApp(platform, state, {
          slug: "alpha",
          domain: "a.example",
          createDatabase: true,
          databaseName: "otherapp_db",
        }),
      Error,
      "namespace",
    );
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("MySQL operations target a selected binding without changing the primary", () => {
  const platform = testPlatform("/tmp/bento-multi-mysql");
  let state = addMysqlVersion(createEmptyState(), "8.0");
  state = provisionApp(platform, state, {
    slug: "alpha",
    domain: "alpha.example",
  }).state;
  state = provisionApp(platform, state, {
    slug: "alpha",
    domain: "alpha.example",
    databaseEngine: "mysql",
    mysqlVersion: "8.0",
  }).state;

  const next = createAppDatabase(
    state,
    "alpha",
    "alpha_archive",
    "2026-07-30T00:00:00Z",
    "mysql80",
  );
  const app = next.apps.alpha!;
  const mysql84 = app.databases.find(
    (binding) => binding.engine === "mysql" && binding.service === "mysql84",
  );
  const mysql80 = app.databases.find(
    (binding) => binding.engine === "mysql" && binding.service === "mysql80",
  );

  assertEquals(mysql84?.engine === "mysql" ? mysql84.databases.length : -1, 0);
  assertEquals(
    mysql80?.engine === "mysql" ? mysql80.databases.map((database) => database.name) : [],
    ["alpha_archive"],
  );
  assertEquals(app.database.engine === "mysql" ? app.database.service : "", "mysql84");
  assertEquals(
    buildMysqlShellPlan(platform, { kind: "app", app }, { service: "mysql80" }).service,
    "mysql80",
  );
});

bunRuntime.test("capacity warnings when pools exceed cap", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState();
    state = {
      ...state,
      phpVersions: state.phpVersions.map((v) => ({ ...v, processCap: 15 })),
    };
    state = provisionApp(platform, state, {
      slug: "app-a",
      domain: "a.example",
      fpmProfile: "medium",
    }).state;
    state = provisionApp(platform, state, {
      slug: "app-b",
      domain: "b.example",
      fpmProfile: "medium",
    }).state;
    const warnings = capacityWarnings(state);
    assertEquals(warnings.length >= 1, true);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("allocateIdentity skips used uids", () => {
  const state = createEmptyState();
  const withApp = {
    ...state,
    apps: {
      x: {
        ...provisionApp(testPlatform("/tmp"), state, {
          slug: "xapp",
          domain: "x.example",
        }).app,
      },
    },
  };
  const first = allocateIdentity(state);
  assertEquals(first, { uid: 10000, gid: 10000 });
  const next = allocateIdentity(withApp);
  assertEquals(next, { uid: 10001, gid: 10001 });
});

bunRuntime.test("workdir escape rejected by path policy", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    assertThrows(
      () => platform.paths.assertInsideHome(join(root, "homes", "app"), "../../etc"),
      Error,
    );
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test(
  "process app provisions a staged private runtime and round-trips strict state",
  () => {
    const platform = testPlatform("/tmp/bento-process-app");
    const result = provisionApp(platform, createEmptyState("2026-08-23T00:00:00.000Z"), {
      slug: "api",
      domain: "api.example",
      kind: "process",
      processLanguage: "node",
      processVersion: "24",
      processCommand: ["node", "server.js"],
      processPort: 8080,
      processHealthPath: "/health",
    });

    assertEquals(result.app.kind, "process");
    if (result.app.kind !== "process") throw new Error("expected process app");
    assertEquals(result.app.enabled, false);
    assertEquals(result.app.runtime.service, "app-api");
    assertEquals(result.app.runtime.image, "bento/node:24");
    assertEquals(result.app.runtime.workdir, "/home/api/code");
    assertEquals(result.app.runtime.command, ["node", "server.js"]);
    assertEquals(result.app.databases.length, 1);
    const parsed = parseDesiredState(JSON.parse(stateToJson(result.state)));
    assertEquals(parsed.ok, true);
    if (!parsed.ok) throw new Error(parsed.errors.join("; "));
    assertEquals(parsed.value.apps.api?.kind, "process");
  },
);

bunRuntime.test("process app home stays private and receives no PHP placeholder", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-process-home-" });
  try {
    const platform = testPlatform(root);
    const app = provisionApp(platform, createEmptyState(), {
      slug: "api",
      domain: "api.example",
      kind: "process",
      processLanguage: "node",
      processVersion: "24",
      processCommand: ["node", "server.js"],
    }).app;
    await materializeAppHome(platform, app);
    const home = platform.paths.appHome(app.slug);
    assertEquals((await platform.fs.stat(home)).mode & 0o777, 0o750);
    assertEquals((await platform.fs.stat(join(home, "code"))).mode & 0o777, 0o750);
    assertEquals(await platform.fs.exists(join(home, "code", "index.php")), false);
    assertEquals(await platform.fs.exists(join(root, "runtime", "apps", "api")), true);
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});

bunRuntime.test("process app compose is private, app-scoped, and exposes a CLI role", () => {
  const platform = testPlatform("/tmp/bento-process-compose");
  const result = provisionApp(platform, createEmptyState(), {
    slug: "worker-api",
    domain: "worker.example",
    kind: "process",
    processLanguage: "python",
    processVersion: "3.13",
    processCommand: ["python", "-m", "http.server", "8080", "--bind", "127.0.0.1"],
  });
  const files = assembleComposeDocuments(platform, result.state);
  const process = String(
    files.find((file) => file.relPath === "compose/docker-compose.app-worker-api.yml")?.content ??
      "",
  );
  assertEquals(process?.includes("app-worker-api:"), true);
  assertEquals(process?.includes("app-worker-api-cli:"), true);
  assertEquals(process?.includes("./homes/worker-api:/home/worker-api"), true);
  assertEquals(process?.includes("./homes:/home"), false);
  assertEquals(process?.includes("ports:"), false);
  assertEquals(process?.includes("disabled-apps"), true);
  assertEquals(process?.includes("/run/bento-http"), true);

  const cli = buildCliExec(platform, result.state, "worker-api", ["python", "--version"]);
  assertEquals(cli.service, "app-worker-api-cli");
  assertEquals(cli.phpVersion, "python@3.13");
});

bunRuntime.test("process apps reject PHP-only webhook deploy", () => {
  const platform = testPlatform("/tmp/bento-process-surfaces");
  const state = provisionApp(platform, createEmptyState(), {
    slug: "api",
    domain: "api.example",
    kind: "process",
    processLanguage: "bun",
    processVersion: "1.2.20",
    processCommand: ["bun", "run", "start"],
  }).state;
  assertThrows(() => enableDeploy(state, { slug: "api" }, platform), Error, "not supported");
});

bunRuntime.test("minicrond CLI targets only the enabled app's runner and numeric identity", () => {
  const state = createEmptyState("2026-07-16T12:00:00.000Z");
  const { state: withApp, app } = provisionApp(testPlatform("/tmp/bento-minicron-test"), state, {
    slug: "alpha",
    domain: "alpha.example",
  });
  const args = ["logs", "a job", "--follow"];
  const command = minicrondComposeCommand(withApp, "alpha", args);
  assertEquals(command.slice(0, 6), [
    "exec",
    "-T",
    "--user",
    `${app.uid}:${app.gid}`,
    "-w",
    app.home,
  ]);
  assertEquals(command.includes(`MINICRON_DATA=${app.home}/.local/share/minicron`), true);
  assertEquals(command.includes(`BASE_PATH=/scheduler/apps/${app.slug}/`), true);
  assertEquals(command.includes(`HOME=${app.home}`), true);
  assertEquals(command.includes(`USER=${app.slug}`), true);
  assertEquals(command.includes("PATH=/usr/local/bin:/usr/bin:/bin"), true);
  assertEquals(command.includes("TZ=UTC"), true);
  assertEquals(command.slice(-5), [`${app.phpService}-runner`, "minicrond", ...args]);
  assertThrows(() => minicrondComposeCommand(withApp, "missing", args));
  assertThrows(() => minicrondComposeCommand(withApp, "__proto__", args));
  assertThrows(() => minicrondComposeCommand(withApp, "alpha", []));
  assertThrows(() => minicrondComposeCommand(withApp, "alpha", ["bad\0arg"]));
  assertThrows(() =>
    minicrondComposeCommand(
      { ...withApp, apps: { alpha: { ...app, enabled: false } } },
      "alpha",
      args,
    ),
  );
});

bunRuntime.test("buildCliExec targets profile-gated -cli service with app identity", async () => {
  const root = await bunRuntime.makeTempDir({ prefix: "bento-test-" });
  try {
    const platform = testPlatform(root);
    let state = createEmptyState("2026-07-16T12:00:00.000Z");
    const provisioned = provisionApp(platform, state, {
      slug: "alpha",
      domain: "alpha.example",
    });
    state = provisioned.state;
    const app = provisioned.app;

    const shell = buildCliExec(platform, state, "alpha", []);
    assertEquals(shell.service, `${app.phpService}-cli`);
    assertEquals(shell.profile, "cli");
    assertEquals(shell.user, `${app.uid}:${app.gid}`);
    assertEquals(shell.workdir, app.home);
    assertEquals(shell.argv, ["bash"]);
    assertEquals(shell.env.HOME, app.home);
    assertEquals(shell.env.BENTO_APP, "alpha");
    assertEquals(shell.env.BENTO_UID, String(app.uid));
    assertEquals(shell.env.BENTO_GID, String(app.gid));
    assertEquals(shell.env.USER, "alpha");

    const runArgs = cliRunComposeCommand(shell, {
      tty: true,
      containerName: "bento-web-shell-test",
    });
    assertEquals(runArgs.slice(0, 5), ["--profile", "cli", "run", "--rm", "-it"]);
    assertEquals(runArgs.includes(shell.service), true);
    assertEquals(runArgs.includes("--name"), true);
    assertEquals(runArgs.includes("bento-web-shell-test"), true);
    // Must NOT use docker -u (that yields "I have no name!"); entrypoint setpriv-drops.
    assertEquals(runArgs.includes("-u"), false);
    assertEquals(runArgs.includes(`BENTO_UID=${app.uid}`), true);
    assertEquals(runArgs.includes(`BENTO_GID=${app.gid}`), true);

    const scripted = cliRunComposeCommand(buildCliExec(platform, state, "alpha", ["php", "-v"]), {
      tty: false,
    });
    assertEquals(scripted.includes("-T"), true);
    assertEquals(scripted.includes("-it"), false);
    assertEquals(scripted.includes("-u"), false);
    assertEquals(scripted.slice(-2), ["php", "-v"]);

    // workdir must stay inside app home
    assertThrows(() => buildCliExec(platform, state, "alpha", [], { workdir: "/etc" }), Error);

    // PHP override must be managed
    state = addPhpVersion(state, "8.3");
    const ov = buildCliExec(platform, state, "alpha", ["php", "-v"], {
      phpVersionOverride: "8.3",
    });
    assertEquals(ov.service, "php83-cli");
    assertEquals(ov.phpVersion, "8.3");
    assertThrows(
      () =>
        buildCliExec(platform, state, "alpha", [], {
          phpVersionOverride: "7.4",
        }),
      Error,
    );
  } finally {
    await bunRuntime.remove(root, { recursive: true });
  }
});
