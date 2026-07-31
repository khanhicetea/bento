import { assertEquals } from "@std/assert";
import type { AppState } from "../../src/domain/state.ts";
import {
  appDatabaseMenuChoices,
  sqliteDatabaseMenuChoices,
} from "../../src/commands/wizard/apps.ts";

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
