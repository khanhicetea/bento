/** PostgreSQL Phase 0 — documentation and acceptance contract only. */

import { assertEquals, assertMatch } from "@std/assert";

const root = new URL("../../", import.meta.url);

async function read(relativePath: string): Promise<string> {
  return await Deno.readTextFile(new URL(relativePath, root));
}

Deno.test("PostgreSQL contract covers multi-binding scope and non-goals", async () => {
  const [product, architecture, contract, readme] = await Promise.all([
    read("specs/01-product-spec.md"),
    read("specs/02-system-architecture.md"),
    read("specs/03-reimplementation-contract.md"),
    read("README.md"),
  ]);

  assertMatch(product, /multiple relational engines\/services/);
  assertMatch(product, /MySQL 8\.4 remains the default/);
  assertMatch(product, /does not automatically migrate application data/);
  assertMatch(
    product,
    /Automated MySQL and PostgreSQL service\/version removal is intentionally unsupported/,
  );
  assertMatch(product, /official major tags such as `17`/);

  assertMatch(
    architecture,
    /discriminated bindings keyed by `engine: "mysql" \| "postgres" \| "sqlite" \| "litestream"`/,
  );
  assertMatch(architecture, /PostgreSQL backup runs matching-major `pg_dump`/);
  assertMatch(architecture, /PostgreSQL administrator credentials/);
  assertMatch(architecture, /Automated MySQL\/PostgreSQL service or volume removal is blocked/);

  assertMatch(contract, /Phase 0 locks this contract only/);
  assertMatch(readme, /PostgreSQL is a first-class database kind alongside MySQL/);
  assertMatch(
    readme,
    /Use logical backup\/restore—not raw volume transfer—for PostgreSQL major upgrades/,
  );
});

Deno.test("PostgreSQL acceptance matrix assigns exactly PG-01 through PG-18", async () => {
  const plan = await read("specs/pg-database.md");
  const gateSection = plan.split("## 10. Final acceptance gates")[1]?.split(
    "## 11. Known risks",
  )[0];
  if (gateSection === undefined) {
    throw new Error("PostgreSQL final acceptance section is missing");
  }

  const gateIds = [...gateSection.matchAll(/^\d+\. \[[ x]\] \*\*(PG-\d{2})\*\*/gm)].map((match) =>
    match[1]
  );
  const expected = Array.from(
    { length: 18 },
    (_, index) => `PG-${String(index + 1).padStart(2, "0")}`,
  );
  assertEquals(gateIds, expected);

  for (const id of expected) {
    assertMatch(gateSection, new RegExp(`\\| ${id.replace("-", "\\-")} \\|`));
  }
  assertMatch(plan, /image `postgres:17`, and volume `postgres17-data`/);
});
