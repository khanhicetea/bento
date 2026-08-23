/** PostgreSQL Phase 0 — documentation and acceptance contract only. */

import { runtime as bunRuntime, assertMatch } from "../runtime.ts";

const root = new URL("../../../../", import.meta.url);

async function read(relativePath: string): Promise<string> {
  return await bunRuntime.readTextFile(new URL(relativePath, root));
}

bunRuntime.test("PostgreSQL contract covers multi-binding scope and non-goals", async () => {
  const [product, architecture, contract, readme] = await Promise.all([
    read("specs/01-product-spec.md"),
    read("specs/02-system-architecture.md"),
    read("specs/03-reimplementation-contract.md"),
    read("README.md"),
  ]);

  assertMatch(product, /MySQL on one managed versioned service/);
  assertMatch(product, /PostgreSQL on one managed major-version service/);
  assertMatch(product, /does not convert or move data between engines\/services/);
  assertMatch(
    product,
    /Managed relational service removal and automatic volume deletion MUST be blocked/,
  );
  assertMatch(product, /Adding a binding MUST preserve existing bindings and data/);

  assertMatch(architecture, /databaseServices\[\]\s+MySQL \| PostgreSQL/);
  assertMatch(architecture, /databases\[\]\s+MySQL \| PostgreSQL \| SQLite \| Litestream/);
  assertMatch(architecture, /PostgreSQL uses unprivileged app roles/);
  assertMatch(
    architecture,
    /refuses destructive Compose volume flags and relational service removal/,
  );

  assertMatch(
    contract,
    /Persist `databases\[\]`; add independent MySQL\/PostgreSQL\/SQLite\/Litestream bindings/,
  );
  assertMatch(readme, /PostgreSQL is a first-class database kind alongside MySQL/);
  assertMatch(
    readme,
    /Use logical backup\/restore—not raw volume transfer—for PostgreSQL major upgrades/,
  );
});

bunRuntime.test("PostgreSQL acceptance requirements remain in the current contracts", async () => {
  const [product, contract] = await Promise.all([
    read("specs/01-product-spec.md"),
    read("specs/03-reimplementation-contract.md"),
  ]);
  assertMatch(product, /PostgreSQL app roles MUST remain unprivileged/);
  assertMatch(product, /Logical backup MUST support one database, one app, or all apps/);
  assertMatch(contract, /MySQL\/PostgreSQL\/Redis and SQLite\/Litestream policy behavior/);
  assertMatch(contract, /MySQL and PostgreSQL connectivity\/isolation\/backup\/restore/);
});
