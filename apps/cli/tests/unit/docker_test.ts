import { join } from "node:path";
import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { createDockerUtils } from "../../src/platform/docker.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";

bunRuntime.test("Docker utilities build a stack-scoped Compose invocation and block destructive down", () => {
  const docker = createDockerUtils("/tmp/my stack", createRecordingProcessRunner());
  assertEquals(docker.compose(["generated/compose/base.yml", "overlays/local.yml"], ["ps", "-q"]), [
    "docker",
    "compose",
    "--project-directory",
    "/tmp/my stack",
    "-f",
    join("/tmp/my stack", "generated/compose/base.yml"),
    "-f",
    join("/tmp/my stack", "overlays/local.yml"),
    "ps",
    "-q",
  ]);
  for (const flag of ["-v", "--volumes", "--rmi", "--rmi=all"]) {
    let refused = false;
    try {
      docker.compose([], ["down", flag]);
    } catch {
      refused = true;
    }
    assertEquals(refused, true);
  }
});

bunRuntime.test("Docker exec keeps arguments literal and scripts/credentials separate", async () => {
  const process = createRecordingProcessRunner();
  const docker = createDockerUtils("/tmp/stack", process);
  assertEquals(docker.execCommand("php85", ["echo", "a; rm -rf /"], true), [
    "docker",
    "compose",
    "exec",
    "-it",
    "php85",
    "echo",
    "a; rm -rf /",
  ]);
  await docker.exec("redis", ["redis-cli", "PING"], { timeoutMs: 1_000 });
  await docker.execScript("mysql84", "cat > /tmp/input", { stdin: "private", timeoutMs: 2_000 });
  assertEquals(process.calls, [
    {
      command: ["docker", "compose", "exec", "-T", "redis", "redis-cli", "PING"],
      options: { cwd: "/tmp/stack", timeoutMs: 1_000 },
    },
    {
      command: ["docker", "compose", "exec", "-T", "mysql84", "sh", "-c", "cat > /tmp/input"],
      options: { cwd: "/tmp/stack", stdin: "private", timeoutMs: 2_000 },
    },
  ]);
});

bunRuntime.test("Docker attach delegates inherited-stdio execution without capturing output", async () => {
  const process = createRecordingProcessRunner();
  const calls: Array<{ command: string[]; cwd: string }> = [];
  const docker = createDockerUtils("/tmp/stack", process, async (command, cwd) => {
    calls.push({ command, cwd });
    return 17;
  });
  assertEquals(await docker.attach(["docker", "compose", "logs", "-f"]), 17);
  assertEquals(calls, [{ command: ["docker", "compose", "logs", "-f"], cwd: "/tmp/stack" }]);
  assertEquals(process.calls, []);
});
