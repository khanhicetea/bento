import { type T } from "../../api/client.ts";
import { Field } from "../../components/DomainState.tsx";
import { Input } from "@/components/ui/input";

const modeText: Record<string, { title: string; detail: string }> = {
  standard: { title: "Standard", detail: "Most apps. Balanced workers and memory per request." },
  "high-concurrency": {
    title: "High concurrency",
    detail: "Apps that mostly wait on APIs or queries. More workers, less memory each.",
  },
};

type Override = "maxWorkers" | "webMemoryLimitMb" | "cliMemoryLimitMb" | "maxExecutionSeconds" | "maxInputVars";

// Same bounds as domain.ValidateRuntime; empty means the mode default.
const bounds: Record<Override, [number, number]> = {
  maxWorkers: [1, 200],
  webMemoryLimitMb: [16, 4096],
  cliMemoryLimitMb: [16, 8192],
  maxExecutionSeconds: [1, 280],
  maxInputVars: [100, 100000],
};

export function phpPerformanceValid(php: T.PHPRuntime) {
  return (Object.keys(bounds) as Override[]).every((key) => {
    const value = php[key];
    return value === undefined || (Number.isInteger(value) && value >= bounds[key][0] && value <= bounds[key][1]);
  });
}

/** Mirrors domain.AutoPHPWorkers. */
function autoWorkers(mode: T.PHPMode, sizing: T.PHPSizing, resources: T.Resources) {
  const share = Math.floor((resources.memoryMb * sizing.webSharePercent) / 100);
  const workers = Math.max(Math.floor(share / sizing.workerMemoryMb), sizing.minWorkers) * mode.workerMultiplier;
  return Math.max(Math.min(workers, Math.floor(resources.pids / 2), sizing.maxWorkers), 1);
}

/** PHP mode choice plus a few optional overrides, sized against the app's memory limit. */
export function PHPPerformance({
  php,
  onChange,
  resources,
  catalog,
}: {
  php: T.PHPRuntime;
  onChange: (php: T.PHPRuntime) => void;
  resources: T.Resources;
  catalog: T.Catalog | undefined;
}) {
  const modes = catalog?.phpModes ?? [];
  const mode = modes.find((m) => m.name === php.mode);
  const sizing = catalog?.phpSizing;
  const auto = mode && sizing ? autoWorkers(mode, sizing, resources) : undefined;
  const workers = php.maxWorkers ?? auto;
  const estimateMb = workers !== undefined && sizing ? workers * sizing.workerMemoryMb : undefined;

  function number(key: Override, label: string, unit: string, fallback: number | undefined) {
    return (
      <Field label={`${label}${unit ? ` (${unit})` : ""}`}>
        <Input
          type="number"
          min={bounds[key][0]}
          max={bounds[key][1]}
          placeholder={fallback === undefined ? "default" : `${fallback}`}
          value={php[key] ?? ""}
          onChange={(event) =>
            onChange({ ...php, [key]: event.target.value === "" ? undefined : Number(event.target.value) })
          }
        />
      </Field>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="choices">
        {modes.map((m) => (
          <label key={m.name} className="choice">
            <input
              className="sr-only"
              type="radio"
              name="php-mode"
              checked={php.mode === m.name}
              onChange={() => onChange({ ...php, mode: m.name })}
            />
            <strong>{modeText[m.name]?.title ?? m.name}</strong>
            <small>{modeText[m.name]?.detail ?? ""}</small>
          </label>
        ))}
      </div>
      {estimateMb !== undefined && (
        <p className={estimateMb > resources.memoryMb * 0.8 ? "text-sm text-destructive" : "note"}>
          ~{workers} workers × ~{sizing?.workerMemoryMb} MB typical ≈ {estimateMb} MB of {resources.memoryMb} MB. The
          rest is shared by nginx, cron jobs and workers.
        </p>
      )}
      <details className="grid gap-3">
        <summary className="note cursor-pointer select-none">Advanced PHP settings</summary>
        <div className="grid-3 mt-3">
          {number("maxWorkers", "Max web workers", "", auto)}
          {number("webMemoryLimitMb", "Web memory limit", "MB", mode?.webMemoryLimitMb)}
          {number("cliMemoryLimitMb", "Jobs & workers memory", "MB", mode?.cliMemoryLimitMb)}
          {number("maxExecutionSeconds", "Max execution time", "s", mode?.maxExecutionSeconds)}
          {number("maxInputVars", "Max input vars", "", mode?.maxInputVars)}
          <Field label="Upload limit (MB)">
            <Input
              type="number"
              min="1"
              max="4096"
              value={php.uploadLimitMb}
              onChange={(event) => onChange({ ...php, uploadLimitMb: Number(event.target.value) })}
            />
          </Field>
        </div>
        <p className="note mt-2">
          Empty fields use the mode default. Other PHP settings can go in a <code>.user.ini</code> in the document root.
        </p>
      </details>
    </div>
  );
}
