-- GET / on every request; prints one RESULT line for run.sh.
-- Count every non-2xx response (wrk's own status errors only count >= 400, so redirects would pass silently).
local threads = {}
setup = function(thread) table.insert(threads, thread) end
non2xx = 0
response = function(status, headers, body)
  if status < 200 or status > 299 then non2xx = non2xx + 1 end
end

done = function(summary, latency, requests)
  local e, bad = summary.errors, 0
  for _, t in ipairs(threads) do bad = bad + t:get("non2xx") end
  io.write(string.format("RESULT req=%d rps=%.2f p50=%.1f p99=%.1f errors=%d non2xx=%d\n",
    summary.requests, summary.requests / (summary.duration / 1e6),
    latency:percentile(50) / 1000, latency:percentile(99) / 1000,
    e.connect + e.read + e.write + e.timeout, bad))
end
