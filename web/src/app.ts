import { createORPCClient } from "@orpc/client";
import { RPCLink } from "@orpc/client/fetch";
import type { InferRouterOutputs } from "@orpc/server";
import type { AppRouter } from "../../src/server/router.ts";

type Snapshot = InferRouterOutputs<AppRouter>["snapshot"];
type Catalog = InferRouterOutputs<AppRouter>["catalog"];

const link = new RPCLink({ url: `${location.origin}/rpc` });
const client = createORPCClient<AppRouter>(link);
const $ = <T extends HTMLElement>(selector: string) => document.querySelector<T>(selector)!;
const content = $("#content");
const notice = $("#notice");
const runner = $("#runner") as HTMLDialogElement;
const outputModal = $("#output-modal") as HTMLDialogElement;
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
  const rows = data.apps
    .map(
      (app) =>
        `<tr><td><strong>${escapeHtml(app.slug)}</strong><br><small>${escapeHtml(app.domain)}</small></td><td>${badge(app.enabled ? "enabled" : "disabled", app.enabled ? "success" : "warning")}</td><td>PHP ${escapeHtml(app.phpVersion)} · ${escapeHtml(app.fpmProfile)}</td><td><div class="pill-row">${app.databases.map((db) => badge(db.engine)).join("")}</div></td><td>${badge(app.tls)}</td><td><button class="btn btn-xs btn-outline run-preset" data-command="app show ${escapeHtml(app.slug)}">Manage</button></td></tr>`,
    )
    .join("");
  content.innerHTML = `<div class="section-head"><div><h2>Applications</h2><p>Domains, runtimes, databases, access logs and deployment.</p></div><button class="btn btn-primary run-preset" data-command="app create <slug> --domain <domain> --docroot public --db">Create app</button></div><div class="table-wrap"><table class="table"><thead><tr><th>Application</th><th>State</th><th>Runtime</th><th>Data</th><th>TLS</th><th></th></tr></thead><tbody>${rows || `<tr><td colspan="6" class="empty">No applications yet.</td></tr>`}</tbody></table></div>
  <div class="card-grid"><section class="panel"><h3>Lifecycle & domains</h3>${commands(["app update <slug> --domain <domain>", "app enable <slug>", "app disable <slug>", "app delete <slug> --confirm 'delete <slug>'", "app prune <slug> --confirm delete", "tls set --app <slug> --mode self-ca"])}</section><section class="panel"><h3>App features</h3>${commands(["deploy status <slug>", "deploy enable <slug>", "deploy disable <slug>", "deploy rotate <slug>", "deploy drain <slug>", "deploy instructions <slug>", "logs access enable --app <slug>", "logs access rotate --app <slug>", "logs access report --app <slug>", "template drift --app <slug>", "template select --app <slug> --kind vhost --source <path>", "template return --app <slug> --kind vhost", "app shell <slug> --print", "exec <slug> -- <command>"])}</section></div>`;
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

async function execute(): Promise<void> {
  const button = $("#run-command") as HTMLButtonElement;
  try {
    const argv = parseArgv($("#command").value);
    if (!argv.length) throw new Error("Enter a Bento command");
    if (argv.some((arg) => /^<.*>$/.test(arg)))
      throw new Error("Replace all <placeholders> with real values");
    if (
      /\b(delete|remove|prune|repair|restore|import)\b/.test(argv.join(" ")) &&
      !confirm(`Run destructive operation?\n\nbento ${argv.join(" ")}`)
    )
      return;
    button.disabled = true;
    button.classList.add("loading");
    const result = await client.execute({ argv });
    runner.close();
    $("#output-title").textContent = `bento ${argv.join(" ")}`;
    $("#exit-code").textContent = result.timedOut ? "timed out" : `exit ${result.code}`;
    $("#exit-code").className = `badge ${result.code === 0 ? "badge-success" : "badge-error"}`;
    $("#output").textContent =
      [result.stdout, result.stderr].filter(Boolean).join("\n") ||
      "Command completed without output.";
    outputModal.showModal();
    await load();
  } catch (error) {
    notice.textContent = error instanceof Error ? error.message : String(error);
  } finally {
    button.disabled = false;
    button.classList.remove("loading");
  }
}

window.addEventListener("hashchange", () => {
  activeView = location.hash.slice(1) || "overview";
  render();
});
$("#refresh").addEventListener("click", load);
$("#menu-button").addEventListener("click", () => $(".sidebar").classList.toggle("open"));
$("#run-command").addEventListener("click", execute);
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
