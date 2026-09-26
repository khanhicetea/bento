import { join } from "node:path";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { afterEach, expect, test } from "bun:test";
import { createContext } from "../../src/commands/context.ts";
import { bootstrapStack } from "../../src/commands/subcommands/core.ts";

const roots: string[] = [];
afterEach(async () => {
  for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true });
});

async function context() {
  const root = await mkdtemp(join(tmpdir(), "bento-bootstrap-test-"));
  roots.push(root);
  return createContext({ stackRoot: root });
}

test("bootstrap first invocation initializes only, second validates required .env before Docker", async () => {
  const ctx = await context();
  const commands: string[][] = [];
  const attach = async (command: string[]) => {
    commands.push(command);
    return 0;
  };
  expect(await bootstrapStack(ctx, "test-stack", attach)).toBe(0);
  expect(commands).toHaveLength(0);
  expect(await ctx.store.exists()).toBe(true);
  const path = join(ctx.stackRoot, ".env");
  const env = await Bun.file(path).text();
  expect(env).toContain("COMPOSE_PROJECT_NAME=test-stack");
  await Bun.write(path, env.replace(/^MYSQL_ROOT_PASSWORD=.*$/m, "MYSQL_ROOT_PASSWORD=  "));
  await expect(bootstrapStack(ctx, undefined, attach)).rejects.toThrow("MYSQL_ROOT_PASSWORD");
  expect(commands).toHaveLength(0);
  await Bun.write(path, env);
  await expect(bootstrapStack(ctx, "different", attach)).rejects.toThrow("stack is named");
  expect(commands).toHaveLength(0);
});

test("bootstrap stops on Compose failure and does not execute reload", async () => {
  const ctx = await context();
  await bootstrapStack(ctx, "test-stack", async () => 0);
  const commands: string[][] = [];
  const code = await bootstrapStack(ctx, undefined, async (command) => {
    commands.push(command);
    return commands.length === 2 ? 23 : 0;
  });
  expect(code).toBe(23);
  expect(commands.map((command) => command.at(-1))).toEqual(["--quiet", "build"]);
  expect(commands[0]).toContain("--project-directory");
  expect(commands[0]).toContain(ctx.stackRoot);
});

test("bootstrap builds local images before starting and pulling missing upstream images", async () => {
  const ctx = await context();
  await bootstrapStack(ctx, "test-stack", async () => 0);
  const calls: string[] = [];
  const compose: string[][] = [];
  // The render service uses the platform process for validation and reload.
  ctx.platform.process.run = async (command) => {
    calls.push(command.join(" "));
    return { code: 0, stdout: "", stderr: "" };
  };
  expect(
    await bootstrapStack(ctx, undefined, async (command) => {
      compose.push(command);
      return 0;
    }),
  ).toBe(0);
  expect(compose.map((command) => command.find((arg) => ["config", "pull", "build", "up"].includes(arg)))).toEqual([
    "config",
    "build",
    "up",
  ]);
  expect(compose.at(-1)?.slice(-3)).toEqual(["up", "-d", "--no-build"]);
  expect(calls.at(-1)).toContain("exec");
});
