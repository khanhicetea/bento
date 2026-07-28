import { assertEquals, assertRejects, assertStringIncludes } from "@std/assert";
import { parse as parseYaml } from "@std/yaml";
import { createEmptyState } from "../../src/domain/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { createAssetResolver } from "../../src/platform/assets.ts";
import { createFixedClock } from "../../src/platform/clock.ts";
import { createFileSystem } from "../../src/platform/fs.ts";
import { createMemoryLock } from "../../src/platform/lock.ts";
import type { Platform } from "../../src/platform/mod.ts";
import { createPathPolicy } from "../../src/platform/paths.ts";
import { createRecordingProcessRunner } from "../../src/platform/process.ts";
import { createSeededRandom } from "../../src/platform/random.ts";
import { assembleComposeDocuments } from "../../src/services/compose.ts";
import {
  loadStackComposeEnvironment,
  parseEnvBoolean,
  updateStackEnv,
  validateComposeProjectName,
} from "../../src/services/stack_env.ts";
import { StateStore } from "../../src/services/state_store.ts";
import { generateAll } from "../../src/services/generate.ts";
import { deployWebhookInstructions } from "../../src/services/deploy.ts";

function testPlatform(root: string): Platform {
  const fs = createFileSystem();
  return {
    clock: createFixedClock("2026-07-28T12:00:00.000Z"),
    random: createSeededRandom("aabbccddeeff0077"),
    fs,
    lock: createMemoryLock(),
    process: createRecordingProcessRunner(),
    assets: createAssetResolver(fs),
    paths: createPathPolicy(root),
  };
}

function baseCompose(
  platform: Platform,
  environment: Parameters<typeof assembleComposeDocuments>[2],
): Record<string, unknown> {
  const generated = assembleComposeDocuments(platform, createEmptyState(), environment).find(
    (file) => file.relPath === "compose/docker-compose.base.yml",
  )!;
  const text = typeof generated.content === "string"
    ? generated.content
    : new TextDecoder().decode(generated.content);
  return parseYaml(text) as Record<string, unknown>;
}

Deno.test("stack init persists an explicit name independent from the stack directory", async () => {
  const root = await Deno.makeTempDir({ prefix: "directory-is-not-stack-name-" });
  try {
    const platform = testPlatform(root);
    const store = new StateStore(platform);
    await store.init(false, { projectName: "customer-a" });
    const environment = await loadStackComposeEnvironment(platform);
    assertEquals(environment.projectName, "customer-a");
    assertEquals(environment.nginx.hostNetwork, true);
    const env = await platform.fs.readText(platform.paths.paths.envFile);
    assertStringIncludes(env, "COMPOSE_PROJECT_NAME=customer-a");
    assertStringIncludes(env, "NGINX_HOST_NETWORK=1");
    assertStringIncludes(env, "NGINX_HTTP_PORT=");
    await assertRejects(
      () => store.init(true, { projectName: "customer-b" }),
      Error,
      "refusing to rename",
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("stack name and ingress environment validation is strict", () => {
  assertEquals(validateComposeProjectName("prod_1"), "prod_1");
  assertEquals(parseEnvBoolean("yes", false, "FLAG"), true);
  assertEquals(parseEnvBoolean("0", true, "FLAG"), false);
  assertRejects(
    async () => validateComposeProjectName("Bad Project"),
    Error,
    "invalid stack name",
  );
  assertRejects(
    async () => parseEnvBoolean("truthy", true, "FLAG"),
    Error,
    "FLAG must be",
  );
});

Deno.test("host mode remains default and does not publish Compose ports", () => {
  const platform = testPlatform("/tmp/unrelated-directory");
  const doc = baseCompose(platform, {
    projectName: "primary",
    nginx: { hostNetwork: true, http3: false },
  });
  const nginx = (doc.services as Record<string, Record<string, unknown>>).nginx!;
  assertEquals(nginx.network_mode, "host");
  assertEquals("networks" in nginx, false);
  assertEquals("ports" in nginx, false);
});

Deno.test("bridge mode joins the private network and publishes stack-selected ports", () => {
  const platform = testPlatform("/tmp/not-the-project-name");
  const doc = baseCompose(platform, {
    projectName: "secondary",
    nginx: {
      hostNetwork: false,
      httpPort: 18080,
      httpsPort: 18443,
      http3: true,
    },
  });
  assertEquals(doc.name, "secondary");
  assertEquals(
    (doc.networks as Record<string, Record<string, unknown>>).private?.name,
    "secondary_private",
  );
  const nginx = (doc.services as Record<string, Record<string, unknown>>).nginx!;
  assertEquals("network_mode" in nginx, false);
  assertEquals(nginx.networks, ["private"]);
  assertEquals(nginx.ports, ["18080:80/tcp", "18443:443/tcp", "18443:443/udp"]);
  assertEquals(nginx.extra_hosts, ["host.docker.internal:host-gateway"]);
});

Deno.test("bridge HTTPS port is advertised in redirects, HTTP/3, and deploy URLs", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-bridge-https-" });
  try {
    const platform = testPlatform(root);
    await new StateStore(platform).init(false, { projectName: "redirect-stack" });
    await updateStackEnv(platform, {
      NGINX_HOST_NETWORK: "0",
      NGINX_HTTP_PORT: "18080",
      NGINX_HTTPS_PORT: "18443",
      HTTP3: "true",
    });
    const provisioned = provisionApp(platform, createEmptyState(), {
      slug: "alpha",
      domain: "alpha.test",
    });
    const state = {
      ...provisioned.state,
      apps: {
        ...provisioned.state.apps,
        alpha: { ...provisioned.app, tls: { kind: "acme" as const } },
      },
    };
    const files = await generateAll(platform, state, "digest");
    const generated = files.find((file) => file.relPath === "nginx/sites/alpha.conf")!;
    const vhost = typeof generated.content === "string"
      ? generated.content
      : new TextDecoder().decode(generated.content);
    assertStringIncludes(vhost, "return 301 https://$host:18443$request_uri;");
    assertStringIncludes(vhost, `Alt-Svc 'h3=\":18443\"`);
    assertStringIncludes(
      deployWebhookInstructions(provisioned.app, "secret", 18443),
      "URL: https://alpha.test:18443/_bento/deploy",
    );
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});

Deno.test("bridge mode can remain internal-only for overlay-owned publications", async () => {
  const root = await Deno.makeTempDir({ prefix: "bento-private-nginx-" });
  try {
    const platform = testPlatform(root);
    await new StateStore(platform).init(false, { projectName: "private-stack" });
    await updateStackEnv(platform, {
      NGINX_HOST_NETWORK: "0",
      NGINX_HTTP_PORT: "",
      NGINX_HTTPS_PORT: "",
    });
    const environment = await loadStackComposeEnvironment(platform);
    const doc = baseCompose(platform, environment);
    const nginx = (doc.services as Record<string, Record<string, unknown>>).nginx!;
    assertEquals(nginx.networks, ["private"]);
    assertEquals("ports" in nginx, false);
  } finally {
    await Deno.remove(root, { recursive: true });
  }
});
