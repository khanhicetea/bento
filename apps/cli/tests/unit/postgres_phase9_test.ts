/** PostgreSQL Phase 9 — public surface, release harness, and documentation contract. */

import { runtime as bunRuntime, assertMatch } from "../runtime.ts";

const root = new URL("../../", import.meta.url);

async function read(path: string): Promise<string> {
  const base = path === "README.md" || path.startsWith("specs/") ? "../../" : "";
  return await bunRuntime.readTextFile(new URL(`${base}${path}`, root));
}

bunRuntime.test(
  "PostgreSQL is registered in the wizard with app and operator workflows",
  async () => {
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
  },
);

bunRuntime.test(
  "test-stack carries PostgreSQL connectivity, isolation, recovery, and transfer proof",
  async () => {
    const harness = await read("src/services/test_stack.ts");
    for (const evidence of [
      /pg-pdo-connect/,
      /pg-isolation/,
      /pg-backup-restore/,
      /Mixed-engine status/,
      /stack-export-mixed/,
      /postgres17-data\.tar\.gz/,
    ]) {
      assertMatch(harness, evidence);
    }
  },
);

bunRuntime.test("release documentation describes shipped PostgreSQL behavior", async () => {
  const [readme, product, architecture, parity] = await Promise.all([
    read("README.md"),
    read("specs/01-product-spec.md"),
    read("specs/02-system-architecture.md"),
    read("tests/contract/parity_test.ts"),
  ]);
  assertMatch(readme, /PostgreSQL is a first-class database kind alongside MySQL/);
  assertMatch(readme, /postgres add 17/);
  assertMatch(product, /does not convert or move data between engines\/services/);
  assertMatch(architecture, /versioned MySQL\/PostgreSQL volumes, Redis volume/);
  assertMatch(parity, /postgres17/);
  assertMatch(parity, /database-engine/);
});
