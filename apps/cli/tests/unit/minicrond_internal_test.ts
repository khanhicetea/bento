import { runtime, assertEquals } from "../runtime.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { provisionApp } from "../../src/services/app.ts";
import { appInternalJobs, minicrondBootstrapConfig, rootInternalJobs } from "../../src/services/minicrond_internal.ts";
import { createPlatform } from "../../src/platform/mod.ts";

runtime.test("internal minicrond config is isolated from user jobs and deterministic", () => {
  const platform = createPlatform("/tmp/bento-minicrond-bundle-test", runtime.cwd());
  const first = provisionApp(platform, createEmptyState(), { slug: "alpha", domain: "alpha.test" });
  const second = provisionApp(platform, first.state, { slug: "beta", domain: "beta.test" });
  const state = second.state;
  state.apps.alpha!.deploy.enabled = true;
  const alpha = appInternalJobs(state, state.apps.alpha!);
  assertEquals(alpha.includes("bento-internal-deploy-drain"), true);
  assertEquals(alpha.includes("/opt/bento/helpers/deploy-drain.sh"), true);
  state.apps.alpha!.deploy.enabled = false;
  assertEquals(appInternalJobs(state, state.apps.alpha!), "");
  assertEquals(appInternalJobs(state, state.apps.beta!), "");
  const root = rootInternalJobs([second.app, first.app]);
  assertEquals(root.indexOf("logrotate-alpha") < root.indexOf("logrotate-beta"), true);
  assertEquals(root.includes("/var/lib/bento/minicron/logrotate-alpha.status"), true);
  assertEquals(minicrondBootstrapConfig(alpha).includes("tcp_enabled = false"), true);
  assertEquals(minicrondBootstrapConfig(root).includes("[[job]]"), true);
});
