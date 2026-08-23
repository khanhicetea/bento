import { createORPCClient } from "@orpc/client";
import { RPCLink } from "@orpc/client/fetch";
import type { ContractRouterClient, InferContractRouterOutputs } from "@orpc/contract";
import type { WebContract } from "@bento/shared";

type Snapshot = InferContractRouterOutputs<WebContract>["snapshot"];
type Catalog = InferContractRouterOutputs<WebContract>["catalog"];

const link = new RPCLink({ url: `${location.origin}/rpc` });
const client = createORPCClient<ContractRouterClient<WebContract>>(link);
const $ = <T extends HTMLElement = HTMLInputElement>(selector: string) =>
  document.querySelector<T>(selector)!;
const content = $("#content");
const notice = $("#notice");
const runner = $("#runner") as HTMLDialogElement;
const outputModal = $("#output-modal") as HTMLDialogElement;
const appEditor = $("#app-editor") as HTMLDialogElement;
const appDetails = $("#app-details") as HTMLDialogElement;
let snapshot: Snapshot | null = null;
let catalog: Catalog = [];
let activeView = location.hash.slice(1) || "overview";

const views = [
  ["overview", "⌂", "Overview"],
  ["apps", "◫", "Applications"],
  ["data", "◆", "Data & runtimes"],
  ["routing", "↗", "Routing & TLS"],
  ["jobs", "↻", "Jobs & workers"],
  ["operations", "✓", "Operations"],
  ["advanced", "›_", "Advanced"],
] as const;

function escapeHtml(value: unknown): string {
  return String(value ?? "").replace(
    /[&<>'"]/g,
    (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" })[char]!,
  );
}
function badge(value: string, kind = "outline"): string {
  return `<span class="badge badge-${kind}">${escapeHtml(value)}</span>`;
}
function commands(values: readonly string[]): string {
  return `<div class="command-list">${values.map((command) => `<div class="command-row"><code>bento ${escapeHtml(command)}</code><button class="btn btn-sm btn-outline run-preset" data-command="${escapeHtml(command)}">Configure</button></div>`).join("")}</div>`;
}

function renderNav(): void {
  $("#nav").innerHTML = views
    .map(
      ([id, icon, label]) =>
        `<button class="nav-link ${activeView === id ? "active" : ""}" data-view="${id}"><span class="nav-icon">${icon}</span>${label}</button>`,
    )
    .join("");
  document.querySelectorAll<HTMLButtonElement>("[data-view]").forEach((button) =>
    button.addEventListener("click", () => {
      location.hash = button.dataset.view!;
      $(".sidebar").classList.remove("open");
    }),
  );
}

async function load(): Promise<void> {
  try {
    [snapshot, catalog] = await Promise.all([client.snapshot({}), client.catalog({})]);
    $("#connection").className = "status status-success";
    $("#connection-label").textContent = "Connected";
    $("#stack-name").textContent = snapshot.projectName || snapshot.stackRoot;
    notice.textContent = "";
    render();
  } catch (error) {
    $("#connection").className = "status status-error";
    $("#connection-label").textContent = "Disconnected";
    notice.textContent = error instanceof Error ? error.message : String(error);
    content.innerHTML = `<div class="alert alert-error">Could not load Bento. Verify the server is running and refresh.</div>`;
  }
}

function render(): void {
  renderNav();
  const title = views.find(([id]) => id === activeView)?.[2] ?? "Overview";
  $("#page-title").textContent = title;
  if (!snapshot) return;
  if (!snapshot.initialized && snapshot.error) {
    content.innerHTML = `<div class="alert alert-error"><div><strong>The selected stack cannot be loaded.</strong><p>${escapeHtml(snapshot.error)}</p><p><code>${escapeHtml(snapshot.stackRoot)}</code></p></div></div><section class="panel full"><h2>Choose a compatible stack</h2><p>Stop the server and restart it with <code>--stack &lt;schema-v1-stack-root&gt;</code>. Bento will not overwrite or migrate an incompatible state file.</p></section>`;
  } else if (!snapshot.initialized && activeView !== "operations" && activeView !== "advanced") {
    content.innerHTML = `<div class="hero"><div><p class="eyebrow">WELCOME TO BENTO</p><h2>Initialize this stack to begin.</h2><p>The web control plane uses the same desired state, validation, and locking as the CLI.</p></div><button class="btn btn-secondary run-preset" data-command="init --name bento">Initialize</button></div>`;
  } else if (!snapshot.initialized) {
    if (activeView === "operations") renderOperations();
    else renderAdvanced();
  } else if (activeView === "overview") renderOverview(snapshot);
  else if (activeView === "apps") renderApps(snapshot);
  else if (activeView === "data") renderData(snapshot);
  else if (activeView === "routing") renderRouting(snapshot);
  else if (activeView === "jobs") renderJobs(snapshot);
  else if (activeView === "operations") renderOperations();
  else renderAdvanced();
  bindPresets();
}

function renderOverview(data: Snapshot & { initialized: true }): void {
  const dbCount = data.apps.reduce((sum, app) => sum + app.databases.length, 0);
  content.innerHTML = `<div class="hero"><div><p class="eyebrow">STACK ${escapeHtml(data.projectName || "BENTO")}</p><h2>Everything your host needs, in one calm workspace.</h2><p>Manage applications, data services, routing and background jobs through Bento's typed oRPC API.</p></div><button class="btn btn-secondary run-preset" data-command="apply --preview">Preview changes</button></div>
  <div class="stats-grid"><div class="metric"><div class="metric-label">Applications</div><div class="metric-value">${data.apps.length}</div></div><div class="metric"><div class="metric-label">Reverse proxies</div><div class="metric-value">${data.proxies.length}</div></div><div class="metric"><div class="metric-label">Database bindings</div><div class="metric-value">${dbCount}</div></div><div class="metric"><div class="metric-label">Background tasks</div><div class="metric-value">${data.cronJobs.length + data.workers.length}</div></div></div>
  <div class="card-grid"><section class="panel"><h2>Quick actions</h2><div class="quick-grid"><button class="btn btn-outline quick run-preset" data-command="status">Stack status</button><button class="btn btn-outline quick run-preset" data-command="doctor">Run doctor</button><button class="btn btn-outline quick run-preset" data-command="apply">Apply config</button></div></section><section class="panel"><h2>Stack</h2><p><strong>Root</strong><br><code>${escapeHtml(data.stackRoot)}</code></p><p><strong>Last state update</strong><br>${escapeHtml(data.updatedAt || "—")}</p></section></div>`;
}

function renderApps(data: Snapshot & { initialized: true }): void {
  const enabled = data.apps.filter((app) => app.enabled).length;
  const appCards = data.apps
    .map(
      (
        app,
      ) => `<article class="app-card" data-app-card data-search="${escapeHtml(`${app.slug} ${app.domain} ${app.aliases.join(" ")}`)}">
        <div class="app-card-head"><div class="app-identity"><span class="app-avatar">${escapeHtml(app.slug.slice(0, 1).toUpperCase())}</span><div><h3>${escapeHtml(app.slug)}</h3><a href="https://${escapeHtml(app.domain)}" target="_blank" rel="noreferrer">${escapeHtml(app.domain)} ↗</a></div></div>${badge(app.enabled ? "Running" : "Disabled", app.enabled ? "success" : "warning")}</div>
        <div class="app-facts"><span><small>Runtime</small><strong>PHP ${escapeHtml(app.phpVersion)}</strong></span><span><small>Capacity</small><strong>${escapeHtml(app.fpmProfile)}</strong></span><span><small>TLS</small><strong>${escapeHtml(app.tls)}</strong></span><span><small>Data</small><strong>${app.databases.length ? app.databases.map((db) => escapeHtml(db.engine)).join(", ") : "None"}</strong></span></div>
        <div class="app-tags">${app.deployEnabled ? badge("Deploys", "success") : ""}${app.accessLog ? badge("Access logs") : ""}${app.aliases
          .slice(0, 2)
          .map((alias) => badge(alias))
          .join("")}</div>
        <div class="app-actions"><button class="btn btn-sm btn-primary" data-app-action="details" data-slug="${escapeHtml(app.slug)}">Manage</button><button class="btn btn-sm btn-outline" data-app-action="edit" data-slug="${escapeHtml(app.slug)}">Edit</button><button class="btn btn-sm btn-ghost" data-app-action="toggle" data-slug="${escapeHtml(app.slug)}">${app.enabled ? "Disable" : "Enable"}</button></div>
      </article>`,
    )
    .join("");
  content.innerHTML = `<div class="section-head"><div><h2>Your applications</h2><p>Manage domains, runtimes and app services without writing commands.</p></div><button class="btn btn-primary" data-app-action="create">＋ New application</button></div>
    <div class="app-summary"><div><strong>${data.apps.length}</strong><span>Total apps</span></div><div><strong>${enabled}</strong><span>Running</span></div><div><strong>${data.apps.reduce((sum, app) => sum + app.databases.length, 0)}</strong><span>Databases</span></div><label class="app-search"><span aria-hidden="true">⌕</span><input id="app-search" class="input" type="search" placeholder="Search apps or domains…" aria-label="Search applications" /></label></div>
    <div id="app-grid" class="app-grid">${appCards || `<div class="empty-state"><div class="empty-icon">◫</div><h3>Create your first application</h3><p>Add a domain, choose a PHP runtime, and Bento will prepare the app.</p><button class="btn btn-primary" data-app-action="create">Create application</button></div>`}</div>
    <p id="app-no-results" class="empty" hidden>No applications match your search.</p>`;
  bindAppManagement(data);
}

function bindAppManagement(data: Snapshot & { initialized: true }): void {
  const handleAction = (button: HTMLButtonElement): void => {
    const action = button.dataset.appAction;
    const app = data.apps.find((item) => item.slug === button.dataset.slug);
    if (action === "create") openAppEditor(data);
    else if (action === "edit" && app) openAppEditor(data, app);
    else if (action === "details" && app) openAppDetails(app);
    else if (action === "toggle" && app)
      void executeArgv(["app", app.enabled ? "disable" : "enable", app.slug], button);
    else if (action === "delete" && app)
      void executeArgv(["app", "delete", app.slug, "--confirm", `delete ${app.slug}`], button);
    else if (action === "deploy" && app)
      void executeArgv(["deploy", app.deployEnabled ? "disable" : "enable", app.slug], button);
    else if (action === "logs" && app)
      void executeArgv(
        ["logs", "access", app.accessLog ? "disable" : "enable", "--app", app.slug],
        button,
      );
    else if (action === "tls" && app) openRunner(`tls set --app ${app.slug} --mode self-ca`);
  };
  content
    .querySelectorAll<HTMLButtonElement>("[data-app-action]")
    .forEach((button) => button.addEventListener("click", () => handleAction(button)));
  appDetails.onclick = (event) => {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>("[data-app-action]");
    if (button) handleAction(button);
  };
  $("#app-search").addEventListener("input", (event) => {
    const query = (event.target as HTMLInputElement).value.trim().toLowerCase();
    let matches = 0;
    content.querySelectorAll<HTMLElement>("[data-app-card]").forEach((card) => {
      const visible = (card.dataset.search || "").toLowerCase().includes(query);
      card.hidden = !visible;
      if (visible) matches++;
    });
    $("#app-no-results").hidden = matches > 0 || !data.apps.length;
  });
}

function openAppEditor(
  data: Snapshot & { initialized: true },
  app?: (Snapshot & { initialized: true })["apps"][number],
): void {
  const editing = Boolean(app);
  appDetails.close();
  $("#app-form-mode").value = editing ? "update" : "create";
  $("#app-editor-title").textContent = editing ? `Edit ${app!.slug}` : "Create application";
  $("#app-editor-help").textContent = editing
    ? "Update the app configuration. Bento will validate and apply your changes."
    : "Set the public domain and runtime. Bento validates and applies the change.";
  const slug = $("#app-slug") as HTMLInputElement;
  slug.value = app?.slug || "";
  slug.readOnly = editing;
  $("#app-domain").value = app?.domain || "";
  $("#app-aliases").value = app?.aliases.join(", ") || "";
  $("#app-docroot").value = "";
  $("#app-routing").value = "";
  const php = $("#app-php") as HTMLSelectElement;
  const versions = [
    ...new Set([...data.phpVersions.map((item) => item.version), ...(app ? [app.phpVersion] : [])]),
  ];
  php.innerHTML = `<option value="">Stack default</option>${versions.map((version) => `<option value="${escapeHtml(version)}">PHP ${escapeHtml(version)}</option>`).join("")}`;
  php.value = app?.phpVersion || "";
  $("#app-fpm").value = app?.fpmProfile || "";
  const db = $("#app-db") as HTMLInputElement;
  db.checked = false;
  $("#database-options").hidden = true;
  $("#app-db-engine").value = "mysql";
  $("#app-db-name").value = "";
  ($("#app-access-log") as HTMLInputElement).checked = app?.accessLog || false;
  $("#save-app").textContent = editing ? "Save changes" : "Create application";
  appEditor.showModal();
  setTimeout(() => (editing ? $("#app-domain") : slug).focus(), 0);
}

function openAppDetails(app: (Snapshot & { initialized: true })["apps"][number]): void {
  const databaseRows = app.databases.length
    ? app.databases
        .map(
          (db) =>
            `<div class="detail-row"><span>${badge(db.engine)}</span><strong>${escapeHtml(db.names.join(", ") || db.file || db.service || "Attached")}</strong></div>`,
        )
        .join("")
    : `<p class="muted">No databases attached.</p>`;
  $("#app-details-content").innerHTML =
    `<p class="eyebrow">APPLICATION</p><div class="details-title"><div><h2>${escapeHtml(app.slug)}</h2><a href="https://${escapeHtml(app.domain)}" target="_blank" rel="noreferrer">${escapeHtml(app.domain)} ↗</a></div>${badge(app.enabled ? "Running" : "Disabled", app.enabled ? "success" : "warning")}</div>
    <div class="details-grid"><section><h3>Configuration</h3><div class="detail-row"><span>PHP runtime</span><strong>${escapeHtml(app.phpVersion)}</strong></div><div class="detail-row"><span>FPM profile</span><strong>${escapeHtml(app.fpmProfile)}</strong></div><div class="detail-row"><span>TLS mode</span><strong>${escapeHtml(app.tls)}</strong></div><div class="detail-row"><span>Aliases</span><strong>${escapeHtml(app.aliases.join(", ") || "None")}</strong></div></section><section><h3>Data</h3>${databaseRows}</section></div>
    <section class="feature-list"><h3>Features</h3><div class="feature-row"><div><strong>Deploy endpoint</strong><small>${app.deployEnabled ? "Accepting deployments" : "Not configured"}</small></div><button class="btn btn-sm btn-outline" data-app-action="deploy" data-slug="${escapeHtml(app.slug)}">${app.deployEnabled ? "Disable" : "Enable"}</button></div><div class="feature-row"><div><strong>Access logs</strong><small>${app.accessLog ? "Per-app request logging is on" : "Use shared server logs"}</small></div><button class="btn btn-sm btn-outline" data-app-action="logs" data-slug="${escapeHtml(app.slug)}">${app.accessLog ? "Disable" : "Enable"}</button></div><div class="feature-row"><div><strong>TLS certificate</strong><small>Configure a locally trusted certificate</small></div><button class="btn btn-sm btn-outline" data-app-action="tls" data-slug="${escapeHtml(app.slug)}">Configure</button></div></section>
    <div class="details-actions"><button class="btn btn-error btn-outline" data-app-action="delete" data-slug="${escapeHtml(app.slug)}">Delete app</button><div><button class="btn btn-ghost" data-app-action="toggle" data-slug="${escapeHtml(app.slug)}">${app.enabled ? "Disable app" : "Enable app"}</button><button class="btn btn-primary" data-app-action="edit" data-slug="${escapeHtml(app.slug)}">Edit configuration</button></div></div>`;
  appDetails.showModal();
}

function renderData(data: Snapshot & { initialized: true }): void {
  content.innerHTML = `<div class="section-head"><div><h2>Data & runtimes</h2><p>PHP, MySQL, PostgreSQL, SQLite and backups.</p></div><button class="btn btn-primary run-preset" data-command="backup --all">Back up all</button></div><div class="stats-grid"><div class="metric"><div class="metric-label">PHP versions</div><div class="metric-value">${data.phpVersions.length}</div><div class="pill-row">${data.phpVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">MySQL</div><div class="metric-value">${data.mysqlVersions.length}</div><div class="pill-row">${data.mysqlVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">PostgreSQL</div><div class="metric-value">${data.postgresVersions.length}</div><div class="pill-row">${data.postgresVersions.map((v) => badge(v.version)).join("")}</div></div><div class="metric"><div class="metric-label">SQLite apps</div><div class="metric-value">${data.apps.filter((a) => a.databases.some((d) => d.engine === "sqlite" || d.engine === "litestream")).length}</div></div></div><div class="card-grid"><section class="panel"><h3>Services</h3>${commands(["php list", "php add <version>", "php reload <version>", "mysql list", "mysql add <version>", "mysql size", "mysql processlist", "mysql shell --app <app> --print", "postgres list", "postgres add <major>", "postgres size", "postgres processlist", "postgres shell --app <app> --print"])}</section><section class="panel"><h3>Database operations</h3>${commands(["mysql db <app> <database>", "postgres db <app> <database>", "sqlite backup local <app> --gzip", "sqlite backup enable <app>", "sqlite backup status", "sqlite backup sync", "sqlite backup verify --app <app>", "sqlite backup export --app <app> --output <path>", "backup --app <slug> --gzip", "restore --file <path> --app <slug>"])}</section></div>`;
}

function renderRouting(data: Snapshot & { initialized: true }): void {
  const rows = data.proxies
    .map(
      (proxy) =>
        `<tr><td><strong>${escapeHtml(proxy.name)}</strong></td><td>${escapeHtml(proxy.domain)}</td><td>${proxy.upstreams.map(escapeHtml).join("<br>")}</td><td>${badge(proxy.tls)}</td><td><button class="btn btn-xs btn-outline run-preset" data-command="tls set --proxy ${escapeHtml(proxy.name)} --mode self-ca">TLS</button></td></tr>`,
    )
    .join("");
  content.innerHTML = `<div class="section-head"><div><h2>Routing & TLS</h2><p>Reverse proxies, upstreams, certificates and ingress.</p></div><button class="btn btn-primary run-preset" data-command="proxy create <name> --domain <domain> --upstream <url>">Create proxy</button></div><div class="table-wrap"><table class="table"><thead><tr><th>Name</th><th>Domain</th><th>Upstreams</th><th>TLS</th><th></th></tr></thead><tbody>${rows || `<tr><td colspan="5" class="empty">No reverse proxies.</td></tr>`}</tbody></table></div><div class="card-grid"><section class="panel"><h3>Routing</h3>${commands(["proxy list", "proxy delete <name> --confirm 'delete <name>'", "stack ingress show", "stack ingress set bridge --http-port 8080 --https-port 8443"])}</section><section class="panel"><h3>Certificates</h3>${commands(["tls set --app <slug> --mode self-ca", "tls set --proxy <name> --mode acme", "tls ca export --output <path>"])}</section></div>`;
}

function renderJobs(data: Snapshot & { initialized: true }): void {
  const taskRows = [
    ...data.cronJobs.map(
      (j) =>
        `<tr><td>${badge("cron")}</td><td>${escapeHtml(j.app)}</td><td>${escapeHtml(j.name)}</td><td><code>${escapeHtml(j.schedule)}</code></td><td>${badge(j.enabled ? "enabled" : "disabled")}</td></tr>`,
    ),
    ...data.workers.map(
      (w) =>
        `<tr><td>${badge("worker")}</td><td>${escapeHtml(w.app)}</td><td>${escapeHtml(w.name)}</td><td><code>${escapeHtml(w.command.join(" "))}</code></td><td>${badge(w.enabled ? "enabled" : "disabled")}</td></tr>`,
    ),
  ].join("");
  content.innerHTML = `<div class="section-head"><div><h2>Jobs & workers</h2><p>Schedules, long-running processes and runtime controls.</p></div></div><div class="table-wrap"><table class="table"><thead><tr><th>Kind</th><th>App</th><th>Name</th><th>Schedule / command</th><th>State</th></tr></thead><tbody>${taskRows || `<tr><td colspan="5" class="empty">No jobs or workers.</td></tr>`}</tbody></table></div><div class="card-grid"><section class="panel"><h3>Cron jobs</h3>${commands(["cron list <app>", "cron add --app <app> --name <name> --schedule '<cron>' --cmd '<command>'", "cron edit <app> <name>", "cron remove <app> <name>", "cron reload <app>"])}</section><section class="panel"><h3>Workers</h3>${commands(["worker list <app>", "worker add --app <app> --name <name> --cmd '<command>'", "worker start <app> <name>", "worker stop <app> <name>", "worker restart <app> <name>", "worker signal <app> <name> --signal HUP", "worker inspect <app> <name>", "worker remove <app> <name>"])}</section></div>`;
}

function renderOperations(): void {
  content.innerHTML = `<div class="section-head"><div><h2>Operations & diagnostics</h2><p>Render, validate, inspect and protect your stack.</p></div></div><div class="card-grid"><section class="panel"><h3>Apply</h3>${commands(["render", "apply --preview", "apply --render-only", "apply"])}</section><section class="panel"><h3>Health & safety</h3>${commands(["status", "doctor", "permissions check", "permissions repair --dry-run", "support-bundle"])}</section><section class="panel"><h3>Maintenance</h3>${commands(["maintenance run", "maintenance register", "maintenance unregister", "backup schedule status", "backup schedule run", "compose files"])}</section><section class="panel"><h3>Stack</h3>${commands(["stack ingress show", "stack export <directory>", "stack import <directory>", "test-stack --skip-build"])}</section></div>`;
}

function renderAdvanced(): void {
  content.innerHTML = `<div class="section-head"><div><h2>Advanced command catalog</h2><p>Every browser-safe management action is sent as a typed argv array—never through a shell.</p></div><button class="btn btn-primary run-preset" data-command="status">Open runner</button></div><div class="card-grid">${catalog.map((group) => `<section class="panel"><h3>${escapeHtml(group.category)}</h3>${commands(group.commands)}</section>`).join("")}</div>`;
}

function bindPresets(): void {
  document
    .querySelectorAll<HTMLButtonElement>(".run-preset")
    .forEach((button) =>
      button.addEventListener("click", () => openRunner(button.dataset.command || "status")),
    );
}
function openRunner(command: string): void {
  appDetails.close();
  $("#command").value = command;
  $("#runner-title").textContent = command.split(" ").slice(0, 2).join(" ");
  runner.showModal();
  setTimeout(() => $("#command").focus(), 0);
}

function parseArgv(value: string): string[] {
  const args: string[] = [];
  let current = "";
  let quote = "";
  let escaped = false;
  for (const char of value.trim()) {
    if (escaped) {
      current += char;
      escaped = false;
      continue;
    }
    if (char === "\\" && quote !== "'") {
      escaped = true;
      continue;
    }
    if (quote) {
      if (char === quote) quote = "";
      else current += char;
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      continue;
    }
    if (/\s/.test(char)) {
      if (current) {
        args.push(current);
        current = "";
      }
    } else current += char;
  }
  if (quote) throw new Error("Close the quoted argument before running");
  if (escaped) current += "\\";
  if (current) args.push(current);
  return args[0] === "bento" ? args.slice(1) : args;
}

async function executeArgv(argv: string[], button?: HTMLButtonElement): Promise<boolean> {
  try {
    notice.textContent = "";
    if (!argv.length) throw new Error("Enter a Bento command");
    if (argv.some((arg) => /^<.*>$/.test(arg)))
      throw new Error("Replace all <placeholders> with real values");
    if (
      /\b(delete|remove|prune|repair|restore|import)\b/.test(argv.join(" ")) &&
      !confirm(`This operation can remove or replace data. Continue?\n\nbento ${argv.join(" ")}`)
    )
      return false;
    if (button) {
      button.disabled = true;
      button.classList.add("loading");
    }
    const result = await client.execute({ argv });
    runner.close();
    appEditor.close();
    appDetails.close();
    $("#output-title").textContent = `bento ${argv.join(" ")}`;
    $("#exit-code").textContent = result.timedOut ? "timed out" : `exit ${result.code}`;
    $("#exit-code").className = `badge ${result.code === 0 ? "badge-success" : "badge-error"}`;
    $("#output").textContent =
      [result.stdout, result.stderr].filter(Boolean).join("\n") ||
      "Command completed without output.";
    outputModal.showModal();
    await load();
    return result.code === 0;
  } catch (error) {
    notice.textContent = error instanceof Error ? error.message : String(error);
    return false;
  } finally {
    if (button) {
      button.disabled = false;
      button.classList.remove("loading");
    }
  }
}

async function execute(): Promise<void> {
  try {
    const argv = parseArgv($("#command").value);
    await executeArgv(argv, $("#run-command") as HTMLButtonElement);
  } catch (error) {
    notice.textContent = error instanceof Error ? error.message : String(error);
  }
}

window.addEventListener("hashchange", () => {
  activeView = location.hash.slice(1) || "overview";
  render();
});
$("#refresh").addEventListener("click", load);
$("#menu-button").addEventListener("click", () => $(".sidebar").classList.toggle("open"));
$("#run-command").addEventListener("click", execute);
$("#cancel-app-form").addEventListener("click", () => appEditor.close());
$("#app-db").addEventListener("change", (event) => {
  $("#database-options").hidden = !(event.target as HTMLInputElement).checked;
});
$("#app-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const value = (selector: string): string =>
    (document.querySelector<HTMLInputElement | HTMLSelectElement>(selector)?.value || "").trim();
  const slug = value("#app-slug");
  const argv = ["app", value("#app-form-mode"), slug, "--domain", value("#app-domain")];
  argv.push(
    "--alias",
    value("#app-aliases")
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean)
      .join(","),
  );
  const optional = [
    ["--php", value("#app-php")],
    ["--fpm", value("#app-fpm")],
    ["--docroot", value("#app-docroot")],
  ] as const;
  for (const [flag, fieldValue] of optional) if (fieldValue) argv.push(flag, fieldValue);
  const routing = value("#app-routing");
  if (routing) argv.push(`--${routing}`);
  if (($("#app-db") as HTMLInputElement).checked) {
    argv.push("--db", "--database-engine", value("#app-db-engine"));
    const databaseName = value("#app-db-name");
    if (databaseName) argv.push("--database", databaseName);
  }
  if (($("#app-access-log") as HTMLInputElement).checked) argv.push("--access-log");
  void executeArgv(argv, $("#save-app") as HTMLButtonElement);
});
$("#copy-output").addEventListener("click", async () => {
  await navigator.clipboard.writeText($("#output").textContent || "");
});
$("#theme").addEventListener("click", () => {
  const next = document.documentElement.dataset.theme === "night" ? "bento" : "night";
  document.documentElement.dataset.theme = next;
  localStorage.setItem("bento-theme", next);
});
document.documentElement.dataset.theme =
  localStorage.getItem("bento-theme") ||
  (matchMedia("(prefers-color-scheme: dark)").matches ? "night" : "bento");
renderNav();
void load();
