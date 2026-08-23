# AGENTS.md

These rules apply to the whole repository. A more specific `AGENTS.md` in a subdirectory overrides them for that subtree.

## Project fundamentals

- Bento is a Bun 1.4 / TypeScript monorepo for a single-host operations control plane.
- Use Bun, not npm, pnpm, or Yarn, for installs and scripts. Keep dependency versions exact and commit `bun.lock` changes.
- Preserve the product and safety contracts in `specs/`. Read the relevant spec before changing behavior.
- Stack roots are external mutable operator state. Never write runtime state beside the source tree or compiled binary.
- Do not edit generated stack output. Change the source state, renderer, or immutable template that owns it.

## Repository boundaries

- `apps/cli`: CLI, oRPC server, domain logic, platform adapters, and services.
- `apps/web`: React 19 browser control plane.
- `packages/shared`: browser-safe schemas, domain types, and oRPC contracts shared by client and server.
- `apps/cli/templates`: immutable assets embedded into compiled releases.
- `specs`: authoritative product, architecture, and reimplementation contracts.
- Keep browser-safe API contracts in `packages/shared`; implement them in domain routers under `apps/cli/src/server/domains`.
- Add web features by domain rather than creating one catch-all API or feature module.

## TypeScript and code style

- Keep TypeScript strict and prefer inferred types at local seams. Do not use `any` unless an external boundary makes it unavoidable.
- Use `.ts` extensions in TypeScript imports and preserve ESM conventions.
- Validate untrusted input at boundaries with Zod or the existing oRPC contract schemas.
- Prefer small domain operations and explicit error handling over shelling out or routing browser actions through CLI argv.
- Use the existing platform abstractions for filesystem, process, clock, and runtime behavior so code remains testable.
- Run Prettier instead of manually formatting around its output.

## React 19 and server state

- Use function components and React 19 APIs.
- Do not add `useMemo`, `useCallback`, or `React.memo`; write direct calculations and ordinary functions. The project relies on React 19-era optimization rather than manual memoization.
- Use `useState` only for local, ephemeral UI state such as form input, disclosure, or theme selection.
- Manage remote/server state with `@tanstack/react-query`; do not reproduce query loading, error, cache, or mutation state with `useEffect` plus `useState`.
- Access oRPC procedures through the typed utilities exported from `apps/web/src/api/client.ts`:
  - queries use `useQuery(orpc.<domain>.<procedure>.queryOptions(...))`;
  - mutations use `useMutation(orpc.<domain>.<procedure>.mutationOptions(...))`;
  - cache reads, updates, and invalidation use oRPC-generated `queryKey`/`key` helpers.
- Keep one `QueryClientProvider` at the web entry point. Do not create a `QueryClient` during component rendering.
- On successful mutations, update or invalidate the narrowest relevant query cache. Do not maintain a second copy of server data in component state.
- Keep presentational components unaware of transport details when a small domain hook provides a clearer boundary.

## Safety and compatibility

- Preserve source/compiled parity and deterministic generated files.
- Never weaken confirmation requirements for destructive operations or allow `compose down -v`.
- Do not expose secrets in logs, diagnostics, command arguments, web responses, or test snapshots. Use existing redaction helpers.
- Keep the web server loopback-safe by default; do not imply that the current unauthenticated UI is safe on an untrusted network.
- Maintain Bun 1.4 compatibility and the supported Linux/compiled-binary paths.

## Tests and required checks

Run the narrowest relevant checks while developing, then the applicable repository gates before handing off:

```bash
bun run fmt
bun run lint
bun run check
bun run test
```

For service, Docker, rendering, or platform behavior, also run the relevant integration tests:

```bash
bun run test:integration
```

For web-only work, at minimum run:

```bash
bun run --cwd apps/web check
bun run --cwd apps/web build
```

Do not claim Docker-dependent or integration checks passed if they were skipped or unavailable. Report changed files, checks run, and any remaining risk.
