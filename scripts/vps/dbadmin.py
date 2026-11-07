"""Open an app binding in Bento's database browser (Adminer) the way the UI does, and check what it shows.

  python3 -I dbadmin.py login                 log in once with the UI password; the session goes to /root/dbmatrix
  python3 -I dbadmin.py open APP_ID BINDING_ID TABLE
                                              ticket, redeem on the utils listener, follow to Adminer; prints the
                                              server Adminer reports and whether TABLE is listed
"""

import http.cookiejar
import json
import re
import sys
import urllib.parse
import urllib.request

UI = "http://127.0.0.1:7780"
STATE = "/root/dbmatrix/session.json"
JAR = "/root/dbmatrix/cookies.txt"


def opener(jar):
    return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def call(op, method, url, body=None, headers=None):
    req = urllib.request.Request(url, method=method, data=None if body is None else json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json", "Origin": UI, **(headers or {})})
    with op.open(req, timeout=30) as r:
        return json.loads(r.read() or b"{}")


def login():
    jar = http.cookiejar.MozillaCookieJar(JAR)
    password = open("/root/.config/bento-test/ui-password").read().strip()
    out = call(opener(jar), "POST", UI + "/api/v1/session", {"password": password})
    jar.save(ignore_discard=True)
    json.dump({"csrf": out.get("csrfToken") or out.get("session", {}).get("csrfToken")}, open(STATE, "w"))
    print("logged in")


def open_binding(app_id, binding_id, table):
    jar = http.cookiejar.MozillaCookieJar(JAR)
    jar.load(ignore_discard=True)
    csrf = json.load(open(STATE))["csrf"]
    op = opener(jar)
    t = call(op, "POST", f"{UI}/api/v1/apps/{app_id}/bindings/{binding_id}/dbadmin", {}, {"X-CSRF-Token": csrf})
    with op.open(f"http://127.0.0.1:{t['loopbackPort']}{t['path']}", timeout=30) as r:
        landing, page = r.geturl(), r.read().decode("utf-8", "replace")
    error = re.search(r"class=['\"]error['\"][^>]*>([^<]{0,200})", page)
    listed = re.search(r">" + re.escape(table) + r"<", page) is not None
    # Run a query through Adminer's SQL command page, with the form token from the page.
    token = re.search(r"name=['\"]token['\"] value=['\"]([^'\"]+)", page)
    server = None
    if token:
        form = urllib.parse.urlencode({"query": "SELECT version()", "token": token.group(1)}).encode()
        origin = f"http://127.0.0.1:{t['loopbackPort']}"  # the gateway takes writes only from its own origin
        req = urllib.request.Request(landing + "&sql=", data=form, method="POST", headers={
            "Origin": origin, "Content-Type": "application/x-www-form-urlencoded"})
        with op.open(req, timeout=30) as r:
            result = r.read().decode("utf-8", "replace")
        cell = re.search(r"<td[^>]*>([^<]*?\d+\.\d+[^<]*)", result)
        server = cell.group(1).strip()[:60] if cell else None
        error = error or re.search(r"class=['\"]error['\"][^>]*>([^<]{0,200})", result)
    print(json.dumps({"server": server, "table": listed, "error": error.group(1) if error else None}))


if __name__ == "__main__":
    if sys.argv[1] == "login":
        login()
    else:
        open_binding(*sys.argv[2:5])
