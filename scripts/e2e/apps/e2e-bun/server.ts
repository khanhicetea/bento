import { Database } from "bun:sqlite";

const db = new Database(process.env.DB_DATABASE!, { create: true });
db.run("CREATE TABLE IF NOT EXISTS hits (id INTEGER PRIMARY KEY AUTOINCREMENT)");

Bun.serve({
  hostname: "0.0.0.0",
  port: Number(process.env.PORT),
  fetch(req) {
    if (new URL(req.url).pathname === "/health") return new Response("ok");
    db.run("INSERT INTO hits DEFAULT VALUES");
    const { n } = db.query("SELECT count(*) AS n FROM hits").get() as { n: number };
    return Response.json({ mark: "bun-v1", bun: Bun.version, hits: n, dbConn: process.env.DB_CONNECTION });
  },
});
