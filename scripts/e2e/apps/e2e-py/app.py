import json, os, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import psycopg

DSN = dict(host=os.environ["DB_HOST"], port=os.environ["DB_PORT"], dbname=os.environ["DB_DATABASE"],
           user=os.environ["DB_USERNAME"], password=os.environ["DB_PASSWORD"])

class H(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            body = b"ok"
        else:
            with psycopg.connect(**DSN) as c:
                c.execute("CREATE TABLE IF NOT EXISTS hits (id serial PRIMARY KEY)")
                c.execute("INSERT INTO hits DEFAULT VALUES")
                n = c.execute("SELECT count(*) FROM hits").fetchone()[0]
            body = json.dumps({"mark": "py-v1", "python": sys.version.split()[0], "psycopg": psycopg.__version__, "hits": n}).encode()
        self.send_response(200); self.send_header("content-type", "application/json"); self.end_headers(); self.wfile.write(body)

ThreadingHTTPServer((os.environ.get("HOST", "0.0.0.0"), int(os.environ["PORT"])), H).serve_forever()
