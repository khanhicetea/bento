#!/usr/bin/env python3
"""Availability probe for the image swap tests (swap.sh).

  probe.py run LOG ADDR HOST[=MUST] ...   request https://HOST/ at ADDR:443 every 100 ms per host until killed;
                                          one line per request: unix-ms host status ms detail
  probe.py report LOG [FROM_MS [TO_MS]]   per host: requests, failures and each outage window

A request fails unless it gets HTTP 200 within 5 s and, with =MUST, the body contains MUST (for example "hits", which
the fixture apps only print after a database round trip). Every request is a new TCP and TLS connection, like a new
visitor, so an edge restart is seen as soon as its listener goes away.
"""
import socket
import ssl
import sys
import threading
import time

INTERVAL = 0.1
TIMEOUT = 5.0


def once(addr, host, must):
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE  # staging certificates
    t0 = time.monotonic()
    try:
        with socket.create_connection((addr, 443), timeout=TIMEOUT) as raw:
            with ctx.wrap_socket(raw, server_hostname=host) as s:
                s.settimeout(TIMEOUT)
                s.sendall(f"GET / HTTP/1.1\r\nHost: {host}\r\nUser-Agent: bento-probe\r\nConnection: close\r\n\r\n".encode())
                buf = b""
                while True:
                    chunk = s.recv(65536)
                    if not chunk:
                        break
                    buf += chunk
                    if len(buf) > 1 << 20:
                        break
    except Exception as e:  # noqa: BLE001 - every failure is a data point
        return 0, (time.monotonic() - t0) * 1000, type(e).__name__ + ":" + str(e)[:60].replace(" ", "_")
    ms = (time.monotonic() - t0) * 1000
    head, _, body = buf.partition(b"\r\n\r\n")
    try:
        status = int(head.split(b" ", 2)[1])
    except (IndexError, ValueError):
        return 0, ms, "bad-response"
    if status != 200:
        return status, ms, "status"
    if must and must.encode() not in body:
        return status, ms, "missing-" + must
    return status, ms, "ok"


def worker(out, lock, addr, spec):
    host, _, must = spec.partition("=")
    while True:
        t = time.time()
        status, ms, detail = once(addr, host, must)
        with lock:
            out.write(f"{int(t * 1000)} {host} {status} {ms:.0f} {detail}\n")
            out.flush()
        time.sleep(max(0.0, INTERVAL - (time.time() - t)))


def run(log, addr, specs):
    lock = threading.Lock()
    with open(log, "a", buffering=1) as out:
        for spec in specs:
            threading.Thread(target=worker, args=(out, lock, addr, spec), daemon=True).start()
        while True:
            time.sleep(3600)


def report(log, lo=0, hi=1 << 62):
    hosts = {}
    with open(log) as f:
        for line in f:
            p = line.split()
            if len(p) < 5 or not lo <= int(p[0]) <= hi:
                continue
            hosts.setdefault(p[1], []).append((int(p[0]), p[2], float(p[3]), p[4]))
    for host, rows in sorted(hosts.items()):
        fails = [r for r in rows if r[3] != "ok"]
        slow = max(r[2] for r in rows)
        print(f"{host}: {len(rows)} requests, {len(fails)} failed, slowest {slow:.0f} ms")
        # Outage windows: consecutive failures; the window runs from the last success before it to the first after.
        win = None
        prev_ok = rows[0][0]
        for t, status, ms, detail in rows:
            if detail != "ok":
                if win is None:
                    win = [prev_ok, t, 0, {}]
                win[1] = t
                win[2] += 1
                key = detail if detail != "status" else f"HTTP{status}"
                win[3][key] = win[3].get(key, 0) + 1
            else:
                if win is not None:
                    print(f"  outage {fmt(win[0])} .. {fmt(t)}  {(t - win[0]) / 1000:.1f} s, {win[2]} failed: {kinds(win[3])}")
                    win = None
                prev_ok = t
        if win is not None:
            print(f"  outage {fmt(win[0])} .. (still failing)  {win[2]} failed: {kinds(win[3])}")


def fmt(ms):
    return time.strftime("%H:%M:%S", time.localtime(ms / 1000)) + f".{ms % 1000:03d}"


def kinds(d):
    return ", ".join(f"{k} x{v}" for k, v in sorted(d.items(), key=lambda kv: -kv[1]))


if __name__ == "__main__":
    if len(sys.argv) >= 5 and sys.argv[1] == "run":
        run(sys.argv[2], sys.argv[3], sys.argv[4:])
    elif len(sys.argv) >= 3 and sys.argv[1] == "report":
        report(sys.argv[2], *[int(a) for a in sys.argv[3:5]])
    else:
        sys.exit(__doc__)
