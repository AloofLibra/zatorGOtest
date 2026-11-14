-- Collect YouTube media CDN hosts seen in TLS SNI into a tmp log
-- that Go merges into /opt/zator/lists/googlevideo-hosts.txt.
-- Pattern: ^rr\d+---sn-[a-z0-9-]+\.googlevideo\.com$
-- Runs as nobody: write only under /tmp/z2r-googlevideo/.

local Z2R_GV_DIR = "/tmp/z2r-googlevideo"
local Z2R_GV_SNI_LOG = Z2R_GV_DIR .. "/sni-hosts.log"
local Z2R_GV_SEEN = {}
local Z2R_GV_SEEN_N = 0
local Z2R_GV_SEEN_MAX = 4096

local function z2r_gv_is_collectable(host)
  if not host or host == "" then return false end
  host = string.lower(tostring(host))
  host = string.gsub(host, "%.$", "")
  -- Media videoplayback CDN only (not redirector / manifests / other GV labels).
  return string.match(host, "^rr%d+%-%-%-sn%-[a-z0-9%-]+%.googlevideo%.com$") ~= nil
end

local function z2r_gv_ensure_dir()
  -- nfqws Lua sandbox often has no os.execute; Go bootstrap creates the dir.
  if type(os.execute) == "function" then
    pcall(os.execute, "mkdir -p " .. Z2R_GV_DIR .. " 2>/dev/null")
  end
end

local function z2r_gv_maybe_collect(host)
  if not z2r_gv_is_collectable(host) then return end
  host = string.lower(tostring(host))
  host = string.gsub(host, "%.$", "")
  if Z2R_GV_SEEN[host] then return end
  if Z2R_GV_SEEN_N >= Z2R_GV_SEEN_MAX then
    Z2R_GV_SEEN = {}
    Z2R_GV_SEEN_N = 0
  end
  Z2R_GV_SEEN[host] = true
  Z2R_GV_SEEN_N = Z2R_GV_SEEN_N + 1
  -- Never throw into circular_*/desync path — collect is best-effort only.
  pcall(function()
    z2r_gv_ensure_dir()
    local f = io.open(Z2R_GV_SNI_LOG, "a")
    if not f then return end
    f:write(host, "\t", tostring(os.time()), "\n")
    f:close()
  end)
end

-- Hook hostname extraction used by locked/circular paths.
if type(desync_hostname) == "function" then
  local _orig_desync_hostname = desync_hostname
  function desync_hostname(desync)
    local h = _orig_desync_hostname(desync)
    if h then pcall(z2r_gv_maybe_collect, h) end
    return h
  end
end

if type(host_or_ip) == "function" then
  local _orig_host_or_ip = host_or_ip
  function host_or_ip(desync)
    local h = _orig_host_or_ip(desync)
    if h then pcall(z2r_gv_maybe_collect, h) end
    return h
  end
end
