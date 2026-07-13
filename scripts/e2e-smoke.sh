#!/usr/bin/env bash
# Boot-and-answer smoke test for agent-store.
#
# Builds cmd/server from THIS checkout, boots it against a throwaway SQLite file
# on a throwaway port, and drives real HTTP routes. Proves the committed tree
# produces a binary that BOOTS and ANSWERS.
#
# "BOOTS" is the whole point here, and this repo is why the tier-2 guard exists.
# agent-store is a LIBRARY that llm-bridge-server mounts into its root mux, and
# it also ships its own server binary. Go 1.22+ ServeMux panics at REGISTRATION
# time on a conflicting route pattern — which compiles perfectly green and then
# takes the host process down at boot. That is exactly what happened when the
# health route moved to /health (see the RegisterHandlers / RegisterHealthHandler
# split in server.go and its doc comment): the gateway paniced on start while
# `go build` and `go vet` stayed happy.
#
#   RegisterHandlers(mux, store)  — registers ONLY agent-store's own namespace
#                                   (/agents, /configs, /reconcile, /files,
#                                   /versions, /seed, /context). No /health.
#   RegisterHealthHandler(mux)    — registers GET /health, for a process that
#                                   owns its own mux (like cmd/server).
#
# The embedded-in-a-host-that-already-owns-/health case is pinned by the unit
# test TestRegisterHandlersEmbedsInHostOwningHealth. What THAT test cannot do is
# prove the shipped binary survives its own registration, because a panic at
# registration only exists in a real process. This smoke is that proof: if any
# route pattern ever conflicts, the process dies before the listener opens and
# every assertion below fails loudly instead of the panic reaching production.
#
# Never touches live state: the live DB is ~/.config/agent-store/agents.db.
# AGENT_STORE_DB is mandatory here — Open("") falls back to that live path and
# migrates it. HOME is redirected too, belt and braces.
#
# AGENT_STORE_SCAN_INTERVAL_SECS=0 is ALSO load-bearing: with it unset, the
# server starts a background auto-scanner that walks $HOME and WRITES tracked
# file versions to the DB. A smoke must not do that.
#
# Exits 0 on success, non-zero on the first failing assertion; dumps the server
# log to stderr on failure.
#
# Tunables:
#   E2E_PORT — agent-store listen port (default 19113)
#   E2E_KEEP — set to "1" to leave $TMP_DIR around after the run

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${E2E_PORT:-19113}"
BASE="http://127.0.0.1:$PORT"

# agent-store uses mattn/go-sqlite3, which is cgo — a C compiler is mandatory or
# `go build` fails with a confusing linker error under the nightly job's minimal env.
for bin in go curl jq cc; do
  if ! command -v "$bin" >/dev/null 2>&1; then
    echo "ERROR: required tool '$bin' not found on PATH" >&2
    exit 2
  fi
done

TMP_DIR="$(mktemp -d -t agent-store-e2e.XXXXXX)"
BIN_DIR="$TMP_DIR/bin"
DATA_DIR="$TMP_DIR/data"
DB_PATH="$DATA_DIR/agents.db"
BODY="$TMP_DIR/body.json"
mkdir -p "$BIN_DIR" "$DATA_DIR" "$TMP_DIR/home"

SLUG="e2e-smoke-$$"

SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "${E2E_KEEP:-}" = "1" ]; then
    echo "[e2e] keeping $TMP_DIR"
  else
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT INT TERM

step() { printf '\n==> %s\n' "$*"; }
fail() {
  echo "FAIL: $*" >&2
  if [ -f "$TMP_DIR/server.log" ]; then
    echo "----- server.log -----" >&2
    cat "$TMP_DIR/server.log" >&2
  fi
  exit 1
}

# req METHOD PATH [JSON] — prints the HTTP status; body → $BODY.
# No -f: 4xx are expected outcomes for some assertions and must not abort.
req() {
  local method="$1" path="$2" data="${3:-}"
  local args=(-sS -o "$BODY" -w '%{http_code}' --max-time 15 -X "$method" "$BASE$path")
  if [ -n "$data" ]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  curl "${args[@]}"
}

expect() {
  local want="$1" got="$2" what="$3"
  if [ "$want" != "$got" ]; then
    echo "----- response body -----" >&2
    cat "$BODY" >&2
    echo >&2
    fail "$what: expected HTTP $want, got $got"
  fi
}

jget() { jq -r "$1" "$BODY"; }

assert_eq() {
  [ "$1" = "$2" ] || fail "$3: expected '$1', got '$2'"
}

# ============================================================================
step "build cmd/server from $REPO_DIR"
cd "$REPO_DIR"
CGO_ENABLED=1 go build -o "$BIN_DIR/agent-store" ./cmd/server
echo "    binary: $(ls -lh "$BIN_DIR/agent-store" | awk '{print $5}')"

# ============================================================================
step "boot agent-store on :$PORT against a throwaway db"
AGENT_STORE_ADDR=":$PORT" \
AGENT_STORE_DB="$DB_PATH" \
AGENT_STORE_SCAN_INTERVAL_SECS=0 \
HOME="$TMP_DIR/home" \
  "$BIN_DIR/agent-store" >>"$TMP_DIR/server.log" 2>&1 &
SERVER_PID=$!
echo "    pid: $SERVER_PID  db: $DB_PATH  auto-scan: off"

# Poll — never sleep-and-hope. Abort the instant the pid dies: a ServeMux
# pattern conflict panics BEFORE the listener opens, and this is the check that
# turns that panic into a clear failure rather than a mystery timeout.
OK=""
for _ in $(seq 1 75); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    fail "agent-store exited during startup — ServeMux route-registration panic? (check server.log below)"
  fi
  if curl -fsS --max-time 2 -o /dev/null "$BASE/health" 2>/dev/null; then OK=1; break; fi
  sleep 0.2
done
[ -n "$OK" ] || fail "agent-store did not answer /health on $BASE within ~15s"

step "GET /health — the route RegisterHealthHandler owns"
CODE=$(req GET /health); expect 200 "$CODE" "/health"
assert_eq ok "$(jget '.status')" "health status"

step "GET /agents — the namespace RegisterHandlers owns; fresh db is empty"
CODE=$(req GET /agents); expect 200 "$CODE" "GET /agents"
assert_eq 0 "$(jget 'length')" "fresh db agent count"

# ============================================================================
step "POST /agents — create an agent and read it back"
# The Agent struct carries NO json tags, so the wire shape is Go field names:
# {"Slug":…,"DisplayName":…}. Single-word keys still decode from lowercase
# ("slug" -> Slug) because encoding/json matches case-insensitively — but it does
# NOT ignore underscores, so "display_name" does NOT reach DisplayName. See the
# snake_case assertion below; that asymmetry is a known defect, not something
# this smoke endorses.
CODE=$(req POST /agents \
  "{\"Slug\":\"$SLUG\",\"DisplayName\":\"E2E Smoke\",\"Role\":\"smoke\",\"Description\":\"created by e2e-smoke\",\"Enabled\":true}")
expect 201 "$CODE" "POST /agents"
assert_eq "$SLUG" "$(jget '.Slug')" "created agent slug"
assert_eq "E2E Smoke" "$(jget '.DisplayName')" "created agent display name"
assert_eq true "$(jget '.Enabled')" "created agent enabled"

step "KNOWN DEFECT pin — a snake_case display_name is silently DROPPED on write"
# GET /agents?expanded=true returns `display_name` (that struct HAS json tags),
# while POST /agents and GET /agents/{slug} speak `DisplayName`. So the read shape
# and the write shape disagree about the name of the same field, and a client
# that posts the snake_case one gets a 201 with an EMPTY display name and no error.
# Nothing on the box does this today, which is why it has survived.
#
# This assertion pins the CURRENT behaviour so the defect cannot quietly change
# under us. Adding json tags to Agent is the fix, but it rewrites the wire format
# of routes dash reads, so it is an API-break decision -- noteboard todo below.
# WHEN THAT IS FIXED, THIS ASSERTION SHOULD FAIL. That is the point: it will make
# whoever fixes it come here, read this, and check dash.
CODE=$(req POST /agents "{\"Slug\":\"$SLUG-snake\",\"display_name\":\"dropped on the floor\"}")
expect 201 "$CODE" "POST /agents with a snake_case display_name"
assert_eq "" "$(jget '.DisplayName')" \
  "snake_case display_name is silently dropped (known defect — see agent-store todo)"
CODE=$(req DELETE "/agents/$SLUG-snake"); expect 200 "$CODE" "clean up the snake_case agent"

step "POST /agents — slug is required"
CODE=$(req POST /agents '{"display_name":"nameless"}')
expect 400 "$CODE" "POST /agents with no slug"

step "GET /agents/{slug} — read the agent back out of sqlite"
CODE=$(req GET "/agents/$SLUG"); expect 200 "$CODE" "GET /agents/{slug}"
assert_eq "$SLUG" "$(jget '.Slug')" "read-back slug"
assert_eq "created by e2e-smoke" "$(jget '.Description')" "read-back description"
assert_eq smoke "$(jget '.Role')" "read-back role"

step "GET /agents — the new agent is in the list, with a status merged in"
CODE=$(req GET /agents); expect 200 "$CODE" "GET /agents"
assert_eq 1 "$(jget 'length')" "agent count"
assert_eq "$SLUG" "$(jget '.[0].Slug')" "listed agent slug"
assert_eq idle "$(jget '.[0].status')" "listed agent default status"

step "PUT /agents/{slug} — update, and the change persists"
CODE=$(req PUT "/agents/$SLUG" '{"DisplayName":"E2E Renamed","Role":"smoke"}')
expect 200 "$CODE" "PUT /agents/{slug}"
CODE=$(req GET "/agents/$SLUG"); expect 200 "$CODE" "GET after PUT"
assert_eq "E2E Renamed" "$(jget '.DisplayName')" "updated display name"

step "GET /agents/{slug} — unknown slug is a 404, not a panic"
CODE=$(req GET /agents/no-such-agent); expect 404 "$CODE" "GET unknown agent"

# ============================================================================
step "GET /reconcile — the read-only diff route answers"
CODE=$(req GET /reconcile); expect 200 "$CODE" "GET /reconcile"
# Shape check only: the body must parse and carry the documented keys.
[ "$(jget 'has("agent_diffs")')" = "true" ] || fail "/reconcile has no agent_diffs key: $(cat "$BODY")"

step "GET /configs — answers"
CODE=$(req GET /configs); expect 200 "$CODE" "GET /configs"

step "GET /context/resolve — routed to its handler, and it validates its input"
# 400 (not 404) proves the pattern matched and the HANDLER ran — i.e. the route
# is really wired, not merely absent-and-falling-through.
CODE=$(req GET /context/resolve); expect 400 "$CODE" "GET /context/resolve with no harness"
jget '.error' | grep -qi 'harness' \
  || fail "/context/resolve 400 did not name the missing harness param: $(cat "$BODY")"

step "GET /seed/profiles — the seed namespace answers"
CODE=$(req GET /seed/profiles); expect 200 "$CODE" "GET /seed/profiles"

step "GET /files — the files namespace answers (auto-scan is off, so it is empty)"
CODE=$(req GET /files); expect 200 "$CODE" "GET /files"

# ============================================================================
step "DELETE /agents/{slug} — and it is really gone"
CODE=$(req DELETE "/agents/$SLUG"); expect 200 "$CODE" "DELETE /agents/{slug}"
CODE=$(req GET "/agents/$SLUG"); expect 404 "$CODE" "GET deleted agent"
CODE=$(req GET /agents); expect 200 "$CODE" "GET /agents after delete"
assert_eq 0 "$(jget 'length')" "agent count after delete"

step "the server is still alive after all of that"
CODE=$(req GET /health); expect 200 "$CODE" "/health at end of run"

# ============================================================================
step "hermeticity: the live db was never opened"
if [ -e "$TMP_DIR/home/.config/agent-store" ]; then
  fail "agent-store wrote to \$HOME/.config/agent-store despite AGENT_STORE_DB — the env var is being ignored"
fi
[ -f "$DB_PATH" ] || fail "the throwaway db was never created at $DB_PATH"
echo "    only $DB_PATH was written"

step "SUCCESS — agent-store boots and answers from this tree"
echo "    server log: $TMP_DIR/server.log"
