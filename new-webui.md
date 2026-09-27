# Bento Web UI — Audit & Rewrite Proposal

Scope: `apps/web/src` (≈3.5k LOC, React 19 + TanStack Query + wouter + shadcn/Tailwind 4).
Status: audit of commit `958cce7`. Constraints from `AGENTS.md` still apply (no `useMemo`/`useCallback`/`memo`,
server state only via React Query, typed client + key factories, exact confirmations never weakened).

---

## 1. Summary verdict

The UI is a thin "form dump" over the REST API. It exposes the backend's nouns faithfully but gives the operator no
sense of *what is happening*, *what needs attention*, or *where they are*. The core problems are structural, not
cosmetic:

1. **No information hierarchy.** There is no overview; every page opens with long prose and a stack of equal-weight
   panels.
2. **Everything is a modal.** App detail, editor, terminal, and restore are dialogs — sometimes nested three deep
   (Detail → Editor / Terminal). There are no URLs for apps, tabs, or operations, so you can't deep-link, refresh,
   or use back.
3. **Operations feedback is detached from what triggered it.** Toasts appear bottom-right, while the row that you
   clicked shows no pending state and the buttons stay clickable.
4. **Forms are raw.** No validation, no dirty tracking, no "what will this change do" preview, and mixed native vs.
   shadcn controls.
5. **Safety gaps** (see §3). One of them breaks an `AGENTS.md` rule.

A rewrite should keep the data layer (`api/client.ts`, `api/keys.ts`, `OperationTracker` invalidation logic) and
replace the shell, navigation model, and page compositions.

---

## 2. Page-by-page findings

### Shell (`components/AppShell.tsx`)
- Top horizontal nav with Unicode glyph "icons" (`◫ ▤ ◧ ↗ ✓`, `☰`, `◐`) even though `lucide-react` is installed.
  They render inconsistently across fonts and carry no meaning.
- Active state uses `location === item.href` — breaks as soon as nested routes exist.
- Mobile menu is a hand-rolled absolute dropdown with ~10 `max-[900px]:` arbitrary variants; no focus trap, no
  close on outside click/Escape.
- Docker health is hidden below 1050px — the single most important signal disappears on laptops/tablets.
- Theme: read from `localStorage` without try/catch, applied in `useEffect` → flash of wrong theme on every load;
  no "system" option.
- Heavy use of arbitrary values (`min-h-[68px]`, `clamp(...)`, `text-[0.84rem]`) instead of the design scale.

### Shared primitives (`components/DomainState.tsx`)
- `PageHeader` renders a breadcrumb "Control plane / X" that duplicates the title verbatim ("Applications /
  Applications") and a paragraph of backend-architecture prose on every page.
- `StateBadge` maps `healthy` and `succeeded` to the *default* (black/primary) badge and `running` to secondary grey.
  Healthy looks the same as a neutral primary chip; there is no green/amber/red semantic colour. Operators cannot
  scan state.
- `Panel` is the only layout unit; every page is "stack of panels" with the same weight.
- `DomainLoading` is a 45vh spinner — causes large layout jumps; no skeletons.

### Applications list (`ApplicationsPage.tsx`)
- Filter lowercases the query but not the haystack (`primaryDomain` may contain uppercase) → missed matches.
- Row actions: no per-row pending state, no disabling while an operation for that app is queued/running, a single
  shared mutation so one error banner appears at the top of the page, unrelated to the row.
- "Desired" and "Observed" columns shown as separate raw strings; the operator must mentally diff them. Drift
  (desired `running`, observed `stopped`) is the *main* thing to surface and isn't highlighted.
- `uid 1003 · app_01H...` sub-line is noise at list level.
- Empty state says "No applications yet." even when the filter just matched nothing.
- No sorting, no state filter chips, no bulk actions, no keyboard navigation.
- Stop/Restart fire immediately with no confirmation on a production app.

### Application detail (`ApplicationDetail.tsx`)
- Modal with `max-h-[92vh] overflow-y-auto` — logs (`55vh`) and scheduler iframe (`60vh`) inside a scrolling modal
  produce nested scrollbars.
- Tab state is local; closing loses it; refresh loses it; no URL.
- Tabs are `Button`s, not a `Tabs` component (no `role="tablist"`, no arrow keys).
- Opening "Edit" or a terminal stacks a second Dialog on top of the first.
- Overview is a flat key/value dump; timestamps are raw ISO strings; container id not truncated/copyable; runtime
  argv is `JSON.stringify` output.
- Data tab: a **single `dbName` state is shared by every binding row** — typing in one input fills all of them.
  After "Add database" succeeds the input is not cleared.
- "Danger" tab mixes a harmless "permissions check" with app removal.
- Scheduler iframe is embedded same-origin inside a dialog.

### Application editor (`ApplicationEditor.tsx`)
- One ~30-field form in a modal, no sections, no progressive disclosure. PHP and HTTP fields swap in place.
- argv entered as raw JSON in a single-line input; validated only on submit.
- Numbers use `Number(e.target.value)` → empty input becomes `0` silently.
- Domains in a raw `<textarea>` (the shadcn `Textarea` exists but isn't used); no per-domain validation or chips.
- Native `<input type="checkbox">` while `components/ui/checkbox.tsx` exists.
- No diff/preview in edit mode even though the description warns that some changes recreate the container. The UI
  should say *which* changed fields are boot-affecting before saving.
- `expectedGeneration` conflict errors surface as a generic alert with no "reload latest" action.

### Data services (`DatabasesPage.tsx`)
- Table is fine but has no link from a service to the apps bound to it, no size/usage, no connection info.
- Hard-coded default versions (`"8.4"`, `"17"`) instead of catalog defaults.
- Redis is mentioned in the description but not addable/visible in a distinct way.

### Backups (`BackupsPage.tsx`)
- Compression select + "upload" checkbox squeezed into the page header as primary actions.
- Artifacts table: raw path as the first column, size always in KiB (a 3 GB dump shows as `3145728.0 KiB`), raw ISO
  dates, no grouping by app/database, no loading or error state.
- Recent runs: no loading/error/empty state.
- Schedule form: cron as free text with no human-readable preview or next-run time; `key={JSON.stringify(data)}`
  reset pattern discards in-progress edits whenever the query refetches.
- Restore: target database is free text; should be a select from the app's actual bindings.

### Ingress (`RoutingPage.tsx`)
- Edge settings: 4-column grid mixing checkboxes and inputs; no dirty indicator; same `key=JSON.stringify` edit-loss
  problem; "Active routes" as a comma-joined string.
- **Reverse proxy "Remove" sends the confirmation phrase automatically** (`api.proxies.remove(name, \`delete ${name}\`)`)
  with one click and no prompt. This violates the "never weaken exact confirmations (`delete <proxy>`)" rule.
- "Disable tunnel" has no confirmation.
- Proxy form is create-only; existing proxies can't be edited; upstreams split by whitespace without validation.

### Operations (`OperationsPage.tsx`)
- "Backend" system info panel lives here instead of on a dashboard.
- List has no filters (state, kind, target), no pagination, raw timestamps, `targetId` instead of the app slug.
- Event log rendered as plain divs; no durations; cancel button has no pending/disabled state.
- "Retained data of removed apps" (a destructive prune flow) is hidden at the bottom of the Operations page.

### Operation tracker toasts (`OperationTracker.tsx`)
- Shows `op.kind` (`app.restart`) rather than a human sentence ("Restarting **shop**").
- Terminal toasts never auto-dismiss; max 5 stack up and cover content (bottom-right is also where table row actions
  live).
- No link from toast to the operation's detail.

### Logs (`LogsPanel.tsx`)
- Toggling follow tears down the EventSource and **clears all lines**.
- Always auto-scrolls to bottom, even when the user scrolled up to read.
- No search/filter, no wrap toggle, no copy/download, no level highlighting.

### Terminal (`TerminalDialog.tsx`)
- Hard-coded dark theme regardless of app theme (fine) but also hard-coded font not loaded anywhere.
- Lives in a dialog stacked on the detail dialog; should be a full-height route or a docked panel.

### Login (`LoginPage.tsx`)
- Acceptable. Missing: caps-lock hint, a note that the UI is loopback-only.

### Cross-cutting
- **Accessibility:** icon-only buttons with glyph text, tabs without ARIA roles, no visible focus management in the
  mobile menu, colour-only state distinction is weak even where it exists.
- **Formatting:** no shared `formatDate`, `formatRelative`, `formatBytes`, `formatDuration` helpers.
- **Consistency:** two ways to build tables (bare `<table>` vs. `components/ui/table.tsx` which is never used), two
  ways to build checkboxes/selects/textareas.
- **Error placement:** errors appear as page-level banners far from the control that caused them.

---

## 3. Safety issues to fix regardless of rewrite

| # | Issue | Location | Fix |
|---|---|---|---|
| S1 | Proxy removal auto-fills `delete <name>` confirmation | `RoutingPage.tsx` `ProxiesPanel` | Confirmation dialog requiring the operator to type `delete <proxy>` |
| S2 | Tunnel disable with one click | `RoutingPage.tsx` `TunnelPanel` | Confirm dialog |
| S3 | Stop/Restart/Unpublish on a live app with one click | `ApplicationsPage.tsx` | Lightweight confirm (no typed phrase needed) for stop/unpublish |
| S4 | Row buttons stay enabled while an op is in flight → duplicate operations | list + detail | Disable per target while it has an active operation |
| S5 | Shared `dbName` across bindings could add a DB to the wrong binding | `ApplicationDetail.tsx` | Per-binding local state (own component) |

---

## 4. Target information architecture

```
/                       Overview (dashboard)
/apps                   Applications list
/apps/new               Create wizard
/apps/:slug             App → Overview tab   (default)
/apps/:slug/logs        App → Logs (full height)
/apps/:slug/terminal    App → Terminal (?mode=tool|running)
/apps/:slug/data        App → Data bindings
/apps/:slug/scheduler   App → Scheduler
/apps/:slug/settings    App → Settings (the editor, sectioned) + Danger zone at the bottom
/data                   Data services (+ /data/:name)
/backups                Backups: Artifacts | Runs | Schedule (tabs)
/ingress                Ingress: Edge | Tunnel | Proxies (tabs)
/activity               Operations (+ /activity/:id)
/system                 Backend/Docker info, retained data (prune), theme
```

Everything addressable by URL. Dialogs are reserved for **confirmations** and short single-purpose forms (add
binding, add service, restore).

### Layout
- **Left sidebar** (collapsible to icons, sheet on mobile) with lucide icons: Overview, Applications, Data,
  Backups, Ingress, Activity, System. Stack name + Docker health pinned at the bottom of the sidebar, always visible.
- **Top bar** inside content: breadcrumb (`Applications / shop / Logs`), global command palette trigger (`⌘K`),
  activity indicator (count of running ops, opens a popover), theme menu (light / dark / system), sign out.
- Content max-width ~1280px for forms; tables and logs may go full width.

---

## 5. Key screens

### 5.1 Overview (new)
- Health strip: Docker, edge, tunnel, backup schedule (last result + next run), each a small stat card with a
  semantic dot.
- **Needs attention** list: apps with drift (desired ≠ observed), `reconcile.blocked`, failed ops in last 24h,
  services initializing, last backup failed. Each item links to the fix location.
- Recent activity (last 10 operations, human sentences, relative time).
- Counts: apps running/total, services, retained data awaiting prune.

### 5.2 Applications list
- Columns: **Name** (slug + primary domain as link), **Status** (single merged status pill: `Running`,
  `Stopped`, `Starting…`, `Drift: wants running, is stopped`, `Blocked`), **Runtime** (icon + `php 8.4` / `node 24`),
  **Ingress** (`Published · managed` / `Private` / `External`), **Activity** (spinner + op name if in flight),
  kebab menu.
- Filter chips: All / Running / Stopped / Needs attention; text search (case-insensitive on slug + all domains);
  sortable columns.
- Row click navigates to `/apps/:slug`. Primary row action is a single context-aware button (Start or Restart);
  everything else in the kebab. Buttons disabled + spinner while that app has an active op.
- Empty states distinguish "no apps — create your first" from "no matches for filter".

### 5.3 App page
- Header: slug, status pill, primary domain (external link), runtime, and action group:
  `Start/Stop`, `Restart`, `Publish/Unpublish`, `Open terminal ▾ (tool shell / exec)`.
- Inline "operation in progress" banner under the header showing the live event stream for ops targeting this app.
- Tabs as real routes (Overview, Logs, Terminal, Data, Scheduler, Settings).
- **Overview**: cards — *Runtime* (container id truncated + copy, started "3h ago" with ISO in tooltip, generation
  current/pending), *Routing* (domains list with primary marker, TLS, redirect, internal URL copyable), *Resources*
  (memory / CPU / pids), *Redis* (user, prefix), *Reconciliation* (only if failures; red card with last error and
  "Retry via restart").
- **Logs**: full-height, sticky toolbar (follow toggle that does *not* clear the buffer, pause-on-scroll-up with
  "Jump to latest" pill, text filter, wrap toggle, copy visible, clear). Virtualize if >2000 lines are kept.
- **Terminal**: full-height inline xterm, mode switch, reconnect, exit code badge.
- **Data**: one card per binding with its own add-database form; add-binding as a small dialog listing only valid
  services; explicit note that bindings are add-only.
- **Settings**: the editor split into sections with a sticky save bar:
  *General* (slug read-only, runtime kind read-only) · *Runtime* · *Resources* · *Routing & TLS* · *Domains*.
  Save bar shows a **change summary** and marks boot-affecting changes ("will recreate the running container").
  Generation conflict → "This app changed since you opened it. Review latest" action.
  **Danger zone** at the bottom: Permissions check/repair (separate, non-destructive card), Remove app (typed
  `delete <slug>` in a dialog listing exactly what is removed vs. retained).

### 5.4 Create wizard (`/apps/new`)
Three steps, each validated before moving on:
1. **Identity & runtime** — slug (live validation + home path preview), runtime cards (PHP-FPM vs HTTP process).
2. **Runtime details** — PHP fields, or HTTP fields with an **argv list editor** (one row per argument, add/remove,
   rendered preview `node server.js`) instead of raw JSON.
3. **Routing & data** — ingress mode as radio cards with one-line explanations, TLS, domains as chip input,
   initial binding, resources under an "Advanced" disclosure with sane defaults.
Final review screen: "Created stopped and unpublished. Next: start → verify → publish."

### 5.5 Backups
- Tabs: **Artifacts** (grouped by app → database, newest first, human size, relative date, restore action),
  **Runs** (state, trigger, duration, artifacts count, upload state, error expandable), **Schedule** (enabled
  switch, cron input with human-readable preview + next 3 run times, retention, rclone remote).
- "Back up now" is a button opening a small dialog (compression, upload) rather than header controls.
- Restore dialog: target database as a select from the app's bindings, warnings as a list, typed
  `replace <db>` confirmation, SQLite "app must be stopped" check shown up front with current app state.

### 5.6 Ingress
- Tabs Edge / Tunnel / Proxies.
- Edge: settings form grouped (Listener: bind/ports/HTTP3 · ACME: email/directory), dirty-aware save bar, active
  routes as a table (domain → app/proxy).
- Tunnel: status card, replace-token form, disable with confirm.
- Proxies: table with edit + remove; create/edit in a dialog; remove requires typed `delete <proxy>`.

### 5.7 Activity
- Filterable table (state, kind, target, origin), paginated, relative times, durations, target shown as app slug
  link.
- `/activity/:id` detail page: timeline of events with levels coloured, guidance callout, cancel button (with
  pending state) for non-terminal ops.

### 5.8 System
- Backend/Docker/arch/root/stack id (copyable).
- **Retained data** list with prune flow (moved from Activity), typed `delete` confirmation unchanged.
- Appearance: theme light/dark/system.

---

## 6. Design system

- **Semantic status tokens**: add `--success`, `--warning`, `--info` (+ foregrounds) alongside `--destructive` in
  `app.css`, light and dark. One `StatusPill` component maps every backend state to `{tone, label, icon}`:
  - success: healthy, succeeded, published
  - info/progress (animated dot): starting, running(op), queued
  - warning: drift, generation pending, blocked-until-restart, interrupted
  - danger: unhealthy, failed
  - neutral: stopped, absent, cancelled, unpublished
- Icons: lucide only. Remove all Unicode glyph icons.
- Use the existing shadcn primitives consistently: `Table`, `Checkbox`, `Textarea`; add `Tabs`, `Switch`,
  `DropdownMenu`, `Tooltip`, `Sheet`, `Skeleton`, `Sonner` (or keep the custom tracker but restyle), `Command`
  (palette), `AlertDialog` (confirmations), `RadioGroup`.
- Typography: page title `text-2xl font-semibold`, section `text-base font-semibold`, no uppercase micro-labels
  except table headers. Remove arbitrary `clamp()`/px values in favour of the Tailwind scale.
- Monospace for ids, paths, domains, argv; always truncatable with copy button.
- Density: 40px table rows, 8px grid spacing.

### Shared helpers (`src/lib/format.ts`)
`formatBytes`, `formatRelative` (with ISO in `title`), `formatDuration`, `formatCron` (human text), `describeOp`
(`app.restart` + target → "Restart shop").

### Shared components to build
`AppLayout` (sidebar + topbar), `PageHeader` (title, optional description ≤1 line, actions), `StatusPill`,
`CopyableCode`, `ConfirmDialog` (plain and typed-phrase variants — the typed variant takes the exact phrase and
never pre-fills), `EmptyState` (icon, title, body, action), `SaveBar` (dirty-aware), `OperationBanner`
(live ops for a target), `DataTable` (thin wrapper over `ui/table` with sorting + empty/loading/error slots).

---

## 7. Behavioural rules for the rewrite

1. **Operations are visible where they were triggered.** A hook `useActiveOperations(target)` (derived from the
   operations list query, filtered by `targetKind/targetId`, non-terminal) drives spinners and disables actions on
   that row/page. Toasts remain for global feedback, auto-dismiss successes after ~6s, keep failures, and link to
   `/activity/:id`.
2. **Errors next to their cause.** Mutation errors render inside the form/dialog/row that issued them.
3. **Forms**: controlled local state seeded from the query once; do not reset via `key={JSON.stringify(...)}` on
   refetch — instead detect "server changed while dirty" and offer to reload. Number inputs keep a string draft and
   validate on blur/submit. Submit disabled until dirty and valid.
4. **Destructive actions** always go through `ConfirmDialog`; exact phrases from `AGENTS.md` unchanged and typed by
   the operator.
5. **Loading**: skeletons matching the final layout; never a 45vh spinner inside a page.
6. **Theme** applied before first paint by a tiny inline script in `index.html` (try/catch around storage), with
   system preference as default.
7. Keep all rules in `AGENTS.md`: React Query for remote state, typed client, key factories, no memo hooks,
   `types.ts` untouched.

---

## 8. Proposed source layout

```
src/
  main.tsx, app.tsx (routes only)
  api/            client.ts, keys.ts, generated/ (unchanged)
  lib/            utils.ts, format.ts, status.ts (state → tone/label/icon)
  components/
    layout/       AppLayout, Sidebar, Topbar, PageHeader, Breadcrumbs, CommandPalette
    feedback/     StatusPill, EmptyState, ErrorState, ConfirmDialog, SaveBar, OperationBanner
    data/         DataTable, CopyableCode, KeyValue
    ui/           shadcn primitives
  features/
    overview/     OverviewPage, AttentionList, HealthStrip
    apps/         AppsListPage, AppLayout(tabs), AppOverview, AppLogs, AppTerminal, AppData,
                  AppScheduler, AppSettings, CreateAppWizard, ArgvEditor, DomainsInput, hooks.ts
    data/         ServicesPage, ServiceDetail
    backups/      BackupsPage, ArtifactsTab, RunsTab, ScheduleTab, RestoreDialog
    ingress/      IngressPage, EdgeTab, TunnelTab, ProxiesTab, ProxyDialog
    activity/     ActivityPage, OperationDetailPage, OperationTracker, useActiveOperations
    system/       SystemPage, RetainedData
    session/      LoginPage, useSession
```

---

## 9. Delivery plan

| Phase | Content | Size |
|---|---|---|
| 0 | Safety fixes S1–S5 in the current UI (small, ship first) | S |
| 1 | Design tokens, `StatusPill`, format helpers, `ConfirmDialog`, `EmptyState`, lucide icons, theme-before-paint | S |
| 2 | New `AppLayout` (sidebar/topbar), URL routing skeleton, move pages behind new routes unchanged | M |
| 3 | Applications list + app page with routed tabs (logs/terminal full-height), `useActiveOperations` | L |
| 4 | Settings form with change summary; create wizard with argv editor and domain chips | L |
| 5 | Backups, Ingress, Activity, System rewrites | M |
| 6 | Overview dashboard + command palette | M |
| 7 | A11y pass (keyboard, ARIA, contrast in both themes), mobile pass at 375px | S |

Each phase must pass `bun run fmt:check && bun run lint && bun run check && bun run web:build`. No backend/DTO
changes are required for phases 0–5; the Overview "needs attention" list can be computed client-side from existing
endpoints (apps, operations, services, backups schedule, system). A dedicated `/api/overview` endpoint is optional
later.

---

## 10. Open questions

- Should app URLs use slug (`/apps/shop`) or id? Slug is friendlier; slugs are permanent per incarnation, but a
  removed-and-recreated slug would reuse the URL — acceptable, since retired apps live under System.
- Keep the custom toast tracker or adopt `sonner`? Recommendation: keep the tracker logic (its invalidation map is
  correct and careful), restyle the presentation.
- Is the scheduler iframe content trusted enough to stay same-origin, or should it always open in a new tab?
