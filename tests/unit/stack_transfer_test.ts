import { assertEquals, assertRejects, assertThrows } from "@std/assert";
import { join } from "@std/path";
import { createEmptyState, type DesiredState } from "../../src/domain/state.ts";
import { isBentoError } from "../../src/domain/errors.ts";
import { createPlatform } from "../../src/platform/mod.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { addPostgresVersion } from "../../src/services/postgres.ts";
import {
  composeProjectName,
  exportStack,
  importStack,
  REDIS_ARCHIVE,
  STACK_ARCHIVE,
  stackDataServiceNames,
  stackVolumeNames,
  volumeArchiveName,
} from "../../src/services/stack_transfer.ts";

function mixedState(): DesiredState {
  return addPostgresVersion(createEmptyState("2026-01-01T00:00:00.000Z"), "17");
}

Deno.test("stack transfer names mixed-engine archives deterministically without collisions", () => {
  assertEquals(STACK_ARCHIVE, "stack.tar.gz");
  assertEquals(volumeArchiveName("mysql84-data"), "mysql84-data.tar.gz");
  assertEquals(volumeArchiveName("postgres17-data"), "postgres17-data.tar.gz");
  assertEquals(volumeArchiveName("redis-data"), REDIS_ARCHIVE);
  assertThrows(() => volumeArchiveName("../escape"), Error, "invalid Docker volume name");

  const names = stackVolumeNames(mixedState(), "customer-stack");
  assertEquals(names.databases, [
    {
      kind: "database",
      engine: "mysql",
      service: "mysql84",
      logical: "mysql84-data",
      docker: "customer-stack_mysql84-data",
    },
    {
      kind: "database",
      engine: "postgres",
      service: "postgres17",
      logical: "postgres17-data",
      docker: "customer-stack_postgres17-data",
    },
  ]);
  assertEquals(names.redis, {
    kind: "redis",
    service: "redis",
    logical: "redis-data",
    docker: "customer-stack_redis-data",
  });
  assertEquals(stackDataServiceNames(mixedState()), ["mysql84", "postgres17", "redis"]);
});

Deno.test("stack transfer rejects duplicate logical volume/archive identities", () => {
  const state = mixedState();
  state.databaseServices[1]!.volume = "mysql84-data";
  assertThrows(() => stackVolumeNames(state, "bento"), Error, "duplicate Docker volume identity");
});

Deno.test("stack transfer validates compose project names", () => {
  assertEquals(composeProjectName({}), "bento");
  assertEquals(composeProjectName({ COMPOSE_PROJECT_NAME: "prod_1" }), "prod_1");
  const err = assertThrows(
    () => composeProjectName({ COMPOSE_PROJECT_NAME: "Bad Project" }),
    Error,
    "invalid COMPOSE_PROJECT_NAME",
  );
  assertEquals(isBentoError(err) && err.code === "VALIDATION", true);
});

Deno.test("mixed-engine export stops and restarts exactly the running data services", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-transfer-export-" });
  const output = await Deno.makeTempDir({ prefix: "bento-transfer-output-" });
  await Deno.remove(output);
  const platform = createPlatform(root, Deno.cwd());
  const state = mixedState();
  await platform.fs.writeText(
    platform.paths.paths.envFile,
    "COMPOSE_PROJECT_NAME=transfer-test\n",
    0o600,
  );
  const process = createRecordingProcessRunner(async (command) => {
    if (command.includes("ps") && command.includes("status=running")) {
      return { code: 0, stdout: "postgres17\nredis\n", stderr: "" };
    }
    if (command[0] === "docker" && command[1] === "run") {
      const target = command.find((part) => part.startsWith("/backup/."));
      if (target) {
        await Deno.writeTextFile(join(output, target.slice("/backup/".length)), "archive");
      }
    }
    if (command[0] === "tar" && command.includes("-czpf")) {
      const target = command[command.indexOf("-czpf") + 1]!;
      await Deno.writeTextFile(target, "stack archive");
    }
    return { code: 0, stdout: "", stderr: "" };
  });
  platform.process = process;

  try {
    const result = await exportStack(platform, state, output);
    assertEquals(result.files.map((path) => path.slice(output.length + 1)), [
      STACK_ARCHIVE,
      "mysql84-data.tar.gz",
      "postgres17-data.tar.gz",
      REDIS_ARCHIVE,
    ]);
    const composeCalls = process.calls.map((call) => call.command).filter((command) =>
      command[0] === "docker" && command[1] === "compose"
    );
    assertEquals(composeCalls.find((command) => command.includes("stop"))?.slice(-3), [
      "stop",
      "postgres17",
      "redis",
    ]);
    assertEquals(composeCalls.find((command) => command.includes("start"))?.slice(-3), [
      "start",
      "postgres17",
      "redis",
    ]);
    assertEquals(
      composeCalls.some((command) => command.includes("mysql84") && command.includes("stop")),
      false,
    );
  } finally {
    await Deno.remove(root, { recursive: true }).catch(() => undefined);
    await Deno.remove(output, { recursive: true }).catch(() => undefined);
  }
});

async function prepareImport(
  mutateSource?: (source: string) => Promise<void>,
  tarListing?: (archive: string) => { code: number; stdout: string; stderr: string },
) {
  const parent = await Deno.makeTempDir({ prefix: "bento-transfer-import-" });
  const root = join(parent, "stack");
  const source = join(parent, "source");
  await Deno.mkdir(source);
  const state = mixedState();
  for (
    const name of [
      STACK_ARCHIVE,
      "mysql84-data.tar.gz",
      "postgres17-data.tar.gz",
      REDIS_ARCHIVE,
    ]
  ) await Deno.writeTextFile(join(source, name), "fixture");
  await mutateSource?.(source);

  const platform = createPlatform(root, Deno.cwd());
  const process = createRecordingProcessRunner(async (command) => {
    if (command[0] === "tar" && command[1] === "-tzf") {
      return tarListing?.(command[2]!) ?? { code: 0, stdout: "./safe/path\n", stderr: "" };
    }
    if (
      command[0] === "tar" && command.includes("-xzpf") &&
      command.some((part) => part.endsWith(`/${STACK_ARCHIVE}`))
    ) {
      await Deno.mkdir(root, { recursive: true });
      await Deno.writeTextFile(join(root, "state.json"), `${JSON.stringify(state, null, 2)}\n`);
      await Deno.writeTextFile(join(root, ".env"), "COMPOSE_PROJECT_NAME=transferimport\n");
    }
    return { code: 0, stdout: "", stderr: "" };
  });
  platform.process = process;
  return { parent, root, source, platform, process };
}

Deno.test("import refuses missing, corrupt, unsafe, and unexpected archives before volume creation", async () => {
  const cases = [
    {
      mutate: (source: string) => Deno.remove(join(source, "postgres17-data.tar.gz")),
      listing: undefined,
      message: "import archive is missing",
    },
    {
      mutate: undefined,
      listing: (archive: string) =>
        archive.endsWith("postgres17-data.tar.gz")
          ? { code: 2, stdout: "", stderr: "corrupt" }
          : { code: 0, stdout: "./safe\n", stderr: "" },
      message: "invalid or corrupt archive",
    },
    {
      mutate: undefined,
      listing: (archive: string) =>
        archive.endsWith("postgres17-data.tar.gz")
          ? { code: 0, stdout: "../escape\n", stderr: "" }
          : { code: 0, stdout: "./safe\n", stderr: "" },
      message: "archive contains unsafe path",
    },
    {
      mutate: (source: string) => Deno.writeTextFile(join(source, "unknown-data.tar.gz"), "x"),
      listing: undefined,
      message: "unexpected archive",
    },
  ];

  for (const testCase of cases) {
    const fixture = await prepareImport(testCase.mutate, testCase.listing);
    try {
      await assertRejects(
        () => importStack(fixture.platform, fixture.source),
        Error,
        testCase.message,
      );
      assertEquals(
        fixture.process.calls.some((call) =>
          call.command[0] === "docker" && call.command[1] === "volume" &&
          call.command[2] === "create"
        ),
        false,
      );
    } finally {
      await Deno.remove(fixture.parent, { recursive: true }).catch(() => undefined);
    }
  }
});

Deno.test("import refuses an existing PostgreSQL volume and cleans up only earlier created volumes", async () => {
  const fixture = await prepareImport();
  fixture.process.calls.length = 0;
  fixture.platform.process = createRecordingProcessRunner(async (command) => {
    if (command[0] === "tar" && command[1] === "-tzf") {
      return { code: 0, stdout: "./safe\n", stderr: "" };
    }
    if (
      command[0] === "tar" && command.includes("-xzpf") &&
      command.some((part) => part.endsWith(`/${STACK_ARCHIVE}`))
    ) {
      await Deno.mkdir(fixture.root, { recursive: true });
      await Deno.writeTextFile(
        join(fixture.root, "state.json"),
        `${JSON.stringify(mixedState(), null, 2)}\n`,
      );
      await Deno.writeTextFile(join(fixture.root, ".env"), "COMPOSE_PROJECT_NAME=transferimport\n");
    }
    if (command.slice(0, 3).join(" ") === "docker volume inspect") {
      return command[3] === "transferimport_postgres17-data"
        ? { code: 0, stdout: "exists", stderr: "" }
        : { code: 1, stdout: "", stderr: "missing" };
    }
    return { code: 0, stdout: "", stderr: "" };
  });
  const process = fixture.platform.process as ReturnType<typeof createRecordingProcessRunner>;

  try {
    await assertRejects(
      () => importStack(fixture.platform, fixture.source),
      Error,
      "destination Docker volume already exists: transferimport_postgres17-data",
    );
    const created = process.calls.filter((call) =>
      call.command.slice(0, 3).join(" ") === "docker volume create"
    )
      .map((call) => call.command[3]);
    const removed = process.calls.filter((call) =>
      call.command.slice(0, 3).join(" ") === "docker volume rm"
    )
      .map((call) => call.command[3]);
    assertEquals(created, ["transferimport_mysql84-data"]);
    assertEquals(removed, ["transferimport_mysql84-data"]);
    assertEquals(removed.includes("transferimport_postgres17-data"), false);
  } finally {
    await Deno.remove(fixture.parent, { recursive: true }).catch(() => undefined);
  }
});
