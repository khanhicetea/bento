import http from "node:http";
import pg from "pg";

const MARK = "node-v1";
const pool = new pg.Pool({
  host: process.env.DB_HOST, port: Number(process.env.DB_PORT),
  database: process.env.DB_DATABASE, user: process.env.DB_USERNAME, password: process.env.DB_PASSWORD,
});

http.createServer(async (req, res) => {
  if (req.url === "/health") { res.end("ok"); return; }
  try {
    await pool.query("CREATE TABLE IF NOT EXISTS hits (id serial PRIMARY KEY, at timestamptz DEFAULT now())");
    await pool.query("INSERT INTO hits DEFAULT VALUES");
    const { rows } = await pool.query("SELECT count(*)::int AS n, version() AS v FROM hits");
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ mark: MARK, node: process.version, hits: rows[0].n, db: rows[0].v.split(" ").slice(0, 2).join(" ") }));
  } catch (e) {
    res.statusCode = 500; res.end("db error: " + e.message);
  }
}).listen(Number(process.env.PORT), process.env.HOST || "0.0.0.0");
