import { runtime as bunRuntime, assertEquals } from "../runtime.ts";
import { basename, join } from "node:path";
import { load as parseYaml } from "js-yaml";
import { runCli } from "../../src/main.ts";

bunRuntime.test(
  "CLI renders two explicitly named stacks with distinct bridge ingress ports",
  async () => {
    const root = await bunRuntime.makeTempDir({
      prefix: "bento-multistack-cli-",
    });
    const first = join(root, "directory-one");
    const second = join(root, "unrelated-directory-two");
    const suffix = basename(root).slice(-12).toLowerCase();
    const blueName = `blue-${suffix}`;
    const greenName = `green-${suffix}`;
    try {
      assertEquals(await runCli(["--stack", first, "init", "--name", blueName]), 0);
      assertEquals(
        await runCli([
          "--stack",
          first,
          "stack",
          "ingress",
          "set",
          "bridge",
          "--http-port",
          "18080",
          "--https-port",
          "18443",
        ]),
        0,
      );
      assertEquals(await runCli(["--stack", second, "init", "--name", greenName]), 0);
      assertEquals(
        await runCli([
          "--stack",
          second,
          "stack",
          "ingress",
          "set",
          "bridge",
          "--http-port",
          "28080",
          "--https-port",
          "28443",
        ]),
        0,
      );

      const readCompose = async (stack: string) =>
        parseYaml(
          await bunRuntime.readTextFile(join(stack, "generated/compose/docker-compose.base.yml")),
        ) as Record<string, unknown>;
      const blue = await readCompose(first);
      const green = await readCompose(second);
      assertEquals(blue.name, blueName);
      assertEquals(green.name, greenName);
      assertEquals(
        (blue.networks as Record<string, Record<string, unknown>>).private?.name,
        `${blueName}_private`,
      );
      assertEquals(
        (green.networks as Record<string, Record<string, unknown>>).private?.name,
        `${greenName}_private`,
      );
      assertEquals(
        (blue.services as Record<string, Record<string, unknown>>).nginx?.ports as string[],
        ["18080:80/tcp", "18443:443/tcp"],
      );
      assertEquals(
        (green.services as Record<string, Record<string, unknown>>).nginx?.ports as string[],
        ["28080:80/tcp", "28443:443/tcp"],
      );
    } finally {
      await bunRuntime.remove(root, { recursive: true });
    }
  },
);
