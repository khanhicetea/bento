import { assertEquals } from "@std/assert";
import type { AppState } from "../../src/domain/state.ts";
import {
  appDatabaseMenuChoices,
  collectAppCreateDraft,
  sqliteDatabaseMenuChoices,
} from "../../src/commands/wizard/apps.ts";
import { createEmptyState } from "../../src/domain/state.ts";
import { type KeyEvent, type TerminalIO, WizardUI } from "../../src/ui/tui.ts";

function appWizardUi(inputs: string[]): { ui: WizardUI; output: string[] } {
  const output: string[] = [];
  const io: TerminalIO = {
    write: (text) => output.push(text),
    writeLine: (text = "") => output.push(text + "\n"),
    readLine: () => Promise.resolve(inputs.shift() ?? null),
    readKey: () => {
      const value = inputs.shift();
      const event: KeyEvent = value === undefined
        ? { type: "eof" }
        : value === ""
        ? { type: "enter" }
        : value === "\x1b"
        ? { type: "escape" }
        : { type: "char", char: value };
      return Promise.resolve(event);
    },
    isInteractive: () => true,
    supportsRawKeys: () => false,
  };
  return { ui: new WizardUI(io), output };
}

Deno.test("app database wizard groups SQLite-backed files", () => {
  const app = {
    slug: "alpha",
    databases: [
      {
        engine: "mysql",
        service: "mysql84",
        user: "alpha",
        password: "mysql-secret",
        databases: [{ name: "alpha", createdAt: "2026-07-30T00:00:00Z" }],
      },
      {
        engine: "postgres",
        service: "postgres17",
        user: "alpha",
        password: "postgres-secret",
        databases: [{ name: "alpha_events", createdAt: "2026-07-30T00:00:00Z" }],
      },
      {
        engine: "sqlite",
        file: {
          id: "alpha_local",
          path: "alpha/alpha_local/app.sqlite3",
          createdAt: "2026-07-30T00:00:00Z",
        },
      },
      {
        engine: "litestream",
        file: {
          id: "alpha_stream",
          path: "alpha/alpha_stream/app.sqlite3",
          createdAt: "2026-07-30T00:00:00Z",
        },
      },
    ],
  } as unknown as AppState;

  const choices = appDatabaseMenuChoices(app);
  assertEquals(
    choices.map((choice) => choice.value),
    [
      "mysql:mysql84",
      "postgres:postgres17",
      "__sqlite",
    ],
  );
  assertEquals(
    choices.map((choice) => choice.label),
    [
      "MySQL · mysql84",
      "PostgreSQL · postgres17",
      "SQLite",
    ],
  );
  assertEquals(choices[2]?.hint, "2 files · 1 local · 1 Litestream");

  const sqliteChoices = sqliteDatabaseMenuChoices(
    app.databases.filter((binding) =>
      binding.engine === "sqlite" || binding.engine === "litestream"
    ),
  );
  assertEquals(
    sqliteChoices.map((choice) => choice.value),
    ["sqlite:alpha_local", "litestream:alpha_stream"],
  );
  assertEquals(
    sqliteChoices.map((choice) => choice.label),
    ["Local · alpha_local", "Litestream · alpha_stream"],
  );
});

Deno.test("app creation draft navigates backward and preserves answers", async () => {
  const inputs = [
    "demo",
    "\x1b",
    "",
    "demo.test",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
  ];
  const { ui, output } = appWizardUi(inputs);
  const state = createEmptyState("2026-07-31T00:00:00.000Z");
  const draft = await collectAppCreateDraft(ui, state);

  assertEquals(draft?.slug, "demo");
  assertEquals(draft?.domain, "demo.test");
  assertEquals(draft?.docroot, "public");
  assertEquals(output.join("").includes("Step 5 of 5 · Review"), true);
});

Deno.test("app review can change one field before apply", async () => {
  const inputs = [
    "demo",
    "demo.test",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "2",
    "2",
    "changed.test",
    "",
  ];
  const { ui } = appWizardUi(inputs);
  const state = createEmptyState("2026-07-31T00:00:00.000Z");
  const draft = await collectAppCreateDraft(ui, state);

  assertEquals(draft?.slug, "demo");
  assertEquals(draft?.domain, "changed.test");
});
