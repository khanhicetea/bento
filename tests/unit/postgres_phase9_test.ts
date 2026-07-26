/** PostgreSQL Phase 9 — public surface, release harness, and documentation contract. */

import { assertMatch } from "@std/assert";

const root = new URL("../../", import.meta.url);

async function read(path: string): Promise<string> {
  return await Deno.readTextFile(new URL(path, root));
}

Deno.test("PostgreSQL is registered in the wizard with app and operator workflows", async () => {
  const [wizard, postgres, apps] = await Promise.all([
    read("src/commands/wizard.ts"),
    read("src/commands/wizard/postgres.ts"),
    read("src/commands/wizard/apps.ts"),
  ]);
  assertMatch(wizard, /Manage PostgreSQL/);
  assertMatch(postgres, /Open shell/);
  assertMatch(postgres, /Logical backup/);
  assertMatch(postgres, /Logical restore/);
  assertMatch(apps, /databaseEngine === "postgres"/);
  assertMatch(apps, /createPostgresAppDatabaseLive/);
  assertMatch(apps, /Open PostgreSQL shell/);
});

Deno.test("test-stack carries PostgreSQL connectivity, isolation, recovery, and transfer proof", async () => {
  const harness = await read("src/services/test_stack.ts");
  for (
    const evidence of [
      /pg-pdo-connect/,
      /pg-isolation/,
      /pg-backup-restore/,
      /Mixed-engine status/,
      /stack-export-mixed/,
      /postgres17-data\.tar\.gz/,
    ]
  ) {
    assertMatch(harness, evidence);
  }
});

Deno.test("release documentation describes shipped PostgreSQL behavior", async () => {
  const [readme, product, architecture, scenarios, parity] = await Promise.all([
    read("README.md"),
    read("specs/01-product-spec.md"),
    read("specs/02-system-architecture.md"),
    read("scripts/system-scenarios.md"),
    read("tests/contract/parity_test.ts"),
  ]);
  assertMatch(readme, /PostgreSQL is a first-class alternative to MySQL/);
  assertMatch(readme, /postgres add 17/);
  assertMatch(product, /exactly one selected MySQL or PostgreSQL service/);
  assertMatch(architecture, /PostgreSQL raw-volume transfer requires a compatible major version/);
  assertMatch(scenarios, /PostgreSQL release scenarios/);
  assertMatch(scenarios, /two PostgreSQL apps/);
  assertMatch(parity, /postgres17/);
  assertMatch(parity, /database-engine/);
});
