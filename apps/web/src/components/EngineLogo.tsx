import { siMysql, siPostgresql, siRedis, siSqlite } from "simple-icons";

const logos: Record<string, { path: string; color: string; title: string }> = {
  mysql: { path: siMysql.path, color: `#${siMysql.hex}`, title: "MySQL" },
  postgres: { path: siPostgresql.path, color: `#${siPostgresql.hex}`, title: "PostgreSQL" },
  // SQLite's brand navy disappears on dark surfaces; use its lighter feather blue.
  sqlite: { path: siSqlite.path, color: "#0F80CC", title: "SQLite" },
  redis: { path: siRedis.path, color: `#${siRedis.hex}`, title: "Redis" },
};

export function EngineLogo({ engine, className = "size-5" }: { engine: string; className?: string }) {
  const logo = logos[engine];
  if (!logo) return null;
  return (
    <svg viewBox="0 0 24 24" className={className} fill={logo.color} role="img" aria-label={logo.title}>
      <path d={logo.path} />
    </svg>
  );
}
