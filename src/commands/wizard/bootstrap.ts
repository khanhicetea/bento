import { describeReloadPlan } from "../../domain/reload.ts";
import { WizardUI } from "../../ui/tui.ts";
import type { CliContext } from "../context.ts";
import { ensureState, handleError } from "./shared.ts";
import {
  DEFAULT_COMPOSE_PROJECT_NAME,
  loadStackComposeEnvironment,
} from "../../services/stack_env.ts";

export async function sectionBootstrap(ui: WizardUI, ctx: CliContext): Promise<void> {
  ui.header("Bootstrap", ctx.stackRoot);
  const actions = [
    { label: "Render generated config (no reload)", value: "render", hint: "bento render" },
    { label: "Apply (render + validate + reload)", value: "apply", hint: "bento apply" },
    {
      label: "Render only (skip service signals)",
      value: "apply-ro",
      hint: "bento apply --render-only",
    },
  ];
  if (!(await ctx.store.exists())) {
    actions.unshift({
      label: "Initialize empty desired state",
      value: "init",
      hint: "bento init",
    });
  }
  const action = await ui.menu("Bootstrap actions", actions);
  if (!action) return;

  if (action === "init") {
    let defaultName = DEFAULT_COMPOSE_PROJECT_NAME;
    if (await ctx.platform.fs.exists(ctx.platform.paths.paths.envFile)) {
      try {
        defaultName = (await loadStackComposeEnvironment(ctx.platform)).projectName;
      } catch {
        // Let init report malformed existing configuration.
      }
    }
    const name = await ui.prompt("Stable stack name", { default: defaultName, required: true });
    if (!name) return;
    try {
      const state = await ctx.store.init({ projectName: name });
      ui.success(
        "Initialized",
        `name=${name}\nstate=${ctx.platform.paths.paths.stateFile}\nphp=${state.defaults.phpVersion} mysql=${state.defaults.database.version}`,
      );
    } catch (err) {
      handleError(ui, err);
    }
  } else if (action === "render") {
    if (!(await ensureState(ui, ctx))) return;
    const state = await ctx.store.load();
    const result = await ctx.render.apply(state, { renderOnly: true, skipValidate: true });
    ui.success(`Rendered ${result.files.length} files`, "render-only, no service signals");
  } else if (action === "apply" || action === "apply-ro") {
    if (!(await ensureState(ui, ctx))) return;
    const renderOnly = action === "apply-ro";
    const skipValidate = renderOnly
      ? true
      : await ui.confirm("Skip config validators?", { defaultYes: false });
    const state = await ctx.store.load();
    const result = await ctx.render.apply(state, { renderOnly, skipValidate });
    ui.success(
      `Applied ${result.files.length} files`,
      `reload=${describeReloadPlan(result.reloadPlan).join(",") || "none"}${
        renderOnly ? " (render-only)" : ""
      }`,
    );
  }
  await ui.pause();
}
