-- Visitor mix over the paths wp-up.sh wrote (BENCH_PATHS): line 1 "/", line 2 "/contact/", line 3 a search,
-- then category archives (lines 4-13), then posts. 20% home, 60% posts, 10% categories, 5% search, 5% contact.
local paths = {}
for line in io.lines(os.getenv("BENCH_PATHS")) do paths[#paths + 1] = line end
local cats, posts = {}, {}
for i = 4, #paths do
  if i <= 13 then cats[#cats + 1] = paths[i] else posts[#posts + 1] = paths[i] end
end

init = function(args)
  math.randomseed(os.time() + tonumber(tostring({}):match("0x(%x+)") or "0", 16) % 100000)
end

request = function()
  local r = math.random(100)
  local p
  if r <= 20 then p = paths[1]
  elseif r <= 80 then p = posts[math.random(#posts)]
  elseif r <= 90 then p = cats[math.random(#cats)]
  elseif r <= 95 then p = paths[3]
  else p = paths[2] end
  return wrk.format("GET", p)
end

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
