#!/bin/zsh
# run-agent-vm.sh — play games in the real app inside a disposable Tart VM, with a custom agent
# endpoint, and collect the scores.
#
# The app window never opens on the host. Everything that launches it happens in a clone of the
# golden image, which is deleted when the script exits. See docs/ui-testing-vm.md.
#
# Per run:
#   1. Preflight: Tart and the golden image, disk, stale system1-agent-<run-id> clones swept.
#   2. Build the app on the host (scripts/build.sh) and stage an export folder: the .app, the
#      agent server (a file or folder), the guest-side player and settings.json.
#   3. Wait for a free macOS VM slot (Apple allows two running at once), clone, boot headless with
#      the export (and optionally a model folder) shared read-only.
#   4. In the guest: install the app, write settings.json (custom agent, batch on), start the agent
#      server, then for each game and seed launch the app with SYSTEM1_AUTOSTART=<game>:<seed>
#      and poll /v1/state until game over or the time cap (scripts/vm-guest-play.py).
#   5. Pull results.jsonl and the logs back to build/agent-vm/<run-id>/, print a summary.
#   6. Cleanup on EXIT: stop and delete the clone, remove the export.
#
# Usage: scripts/run-agent-vm.sh [options]
#   --games LIST        comma-separated game ids (default: frogger)
#   --seeds LIST        comma-separated seeds (default: 1)
#   --cap SECS          seconds of play per game before stopping it (default: 300)
#   --server PATH       agent server file or folder to copy into the guest
#                       (default: agents/custom_agent_example.py)
#   --server-cmd CMD    command that starts it, run in the copied folder with $PORT and
#                       $MODEL_DIR set (default: python3 custom_agent_example.py --port $PORT)
#   --server-timeout S  seconds to wait for the server to answer (default: 300)
#   --model-dir DIR     folder shared read-only into the guest as $MODEL_DIR (symlinks resolved)
#   --port N            agent server port in the guest (default: 8000)
#   --agent-url URL     the app's custom agent URL (default: http://127.0.0.1:<port>/predict), e.g.
#                       a server on the host at http://192.168.64.1:8000/predict to compare with one
#                       in the guest; --server still starts (pass --server-cmd 'sleep 86400' for none)
#   --input-rate N      Settings' agent input speed, inputs per second, 0 = full (default: 6)
#   --no-batch          send one state per request (Settings' batch switch off)
#   --verify FILE       JSONL of {"request", "status", "response"}: during play (one every
#                       --verify-every seconds) and after it (all of them) the guest posts each
#                       request to the agent and compares the reply (choices equal, numbers
#                       within 1.5e-4)
#   --verify-every S    seconds between checks during play, 0 = only after (default: 10)
#   --memory MB         guest memory (default: 8192)
#   --slot-timeout S    how long to wait for a free VM slot (default: 3600)
#   --no-build          use the existing build/bin/System 1 Arcade.app
#   --keep-export       keep the staged export folder (for debugging)
#
# Example, a Go laya-server binary with the Laya weights:
#   scripts/run-agent-vm.sh --games frogger,tetris,invaders --seeds 1,2,3 --cap 600 \
#     --server ../laya-go/bin/laya-server --server-cmd './laya-server --port $PORT --model "$MODEL_DIR"' \
#     --model-dir ~/.cache/huggingface/hub/models--convaiinnovations--laya/snapshots/<revision>
#   (Point --model-dir at one snapshot, not the whole cache: symlinks are resolved, so the cache
#   root would bring every revision plus blobs/.)
#
# Run it in the foreground of a shell; a dropped session costs a clone, nothing more.

set -euo pipefail

GOLDEN="system1-uitest-golden"
GOLDEN_FROM="curator-uitest-golden"   # created from this once if missing; never changed
PREFIX="system1-agent"
REPO="${0:A:h:h}"
APP="$REPO/build/bin/System 1 Arcade.app"
GUEST_HOME="/Users/admin"
GUEST_RESULTS="$GUEST_HOME/results"
SHARE="/Volumes/My Shared Files"
RUN_ID="$(date +%Y%m%d-%H%M%S)-$$"
CLONE="$PREFIX-$RUN_ID"
RESULTS_DIR="$REPO/build/agent-vm/$RUN_ID"
BOOT_TIMEOUT_SECS=180
MAX_MACOS_VMS=${SYSTEM1_VM_SLOTS:-2}   # Apple's limit; lower only to test the wait

GAMES="frogger" SEEDS="1" CAP=300 PORT=8000 INPUT_RATE=6 BATCH=true MEMORY_MB=8192
SERVER="$REPO/agents/custom_agent_example.py"
SERVER_CMD='python3 custom_agent_example.py --port $PORT'
SERVER_TIMEOUT=300 MODEL_DIR="" SLOT_TIMEOUT=3600 BUILD=1 KEEP_EXPORT=0 VERIFY="" VERIFY_EVERY=10

log()  { print -r -- "==> $*"; }
fail() { print -r -- "!! $*" >&2; exit 1; }
usage() { sed -n '/^# Usage:/,/^# Run it/p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while (( $# )); do
  case "$1" in
    --games) GAMES="$2"; shift ;;
    --seeds) SEEDS="$2"; shift ;;
    --cap) CAP="$2"; shift ;;
    --server) SERVER="${2:A}"; shift ;;
    --server-cmd) SERVER_CMD="$2"; shift ;;
    --server-timeout) SERVER_TIMEOUT="$2"; shift ;;
    --model-dir) MODEL_DIR="${2:A}"; shift ;;
    --port) PORT="$2"; shift ;;
    --agent-url) AGENT_URL="$2"; shift ;;
    --input-rate) INPUT_RATE="$2"; shift ;;
    --no-batch) BATCH=false ;;
    --verify) VERIFY="${2:A}"; shift ;;
    --verify-every) VERIFY_EVERY="$2"; shift ;;
    --memory) MEMORY_MB="$2"; shift ;;
    --slot-timeout) SLOT_TIMEOUT="$2"; shift ;;
    --no-build) BUILD=0 ;;
    --keep-export) KEEP_EXPORT=1 ;;
    -h|--help) usage ;;
    *) print -r -- "unknown option: $1" >&2; usage 2 ;;
  esac
  shift
done

AGENT_URL="${AGENT_URL:-http://127.0.0.1:$PORT/predict}"
STAGE="$(mktemp -d "${TMPDIR:-/tmp}/$PREFIX-stage.XXXXXX")"
EXPORT="$STAGE/run"   # shared read-only as "run"
mkdir -p "$EXPORT"
TART_PID=""
LEASE_ID=""
T0=$SECONDS
typeset -A T   # phase timings, seconds

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  log "Cleaning up (exit $rc)"
  if [[ -n "${LEASE_ID:-}" ]]; then
    tart-lease release --id "$LEASE_ID" 2>/dev/null || true
    LEASE_ID=""
  fi
  tart stop "$CLONE" >/dev/null 2>&1 || true
  [[ -n "$TART_PID" ]] && kill "$TART_PID" 2>/dev/null || true
  tart delete "$CLONE" >/dev/null 2>&1 || true
  if (( KEEP_EXPORT )); then log "Export kept at $STAGE"; else rm -rf "$STAGE"; fi
  exit $rc
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# --- Preflight ---------------------------------------------------------------

command -v tart >/dev/null || fail "Tart is not installed (brew install cirruslabs/cli/tart)"
[[ -e "$SERVER" ]] || fail "Agent server not found: $SERVER"
[[ -z "$MODEL_DIR" || -d "$MODEL_DIR" ]] || fail "Model folder not found: $MODEL_DIR"
[[ -z "$VERIFY" || -f "$VERIFY" ]] || fail "Verify file not found: $VERIFY"

# Not grep -q: it exits at the first match, and under pipefail the SIGPIPE to tart fails the test.
vm_exists() { tart list --quiet 2>/dev/null | grep -x -- "$1" >/dev/null; }

if ! vm_exists "$GOLDEN"; then
  vm_exists "$GOLDEN_FROM" || fail "Neither $GOLDEN nor $GOLDEN_FROM exists (see docs/ui-testing-vm.md)"
  log "Creating $GOLDEN from $GOLDEN_FROM (APFS copy-on-write; $GOLDEN_FROM is not changed)"
  tart clone "$GOLDEN_FROM" "$GOLDEN"
fi

free_kb=$(df -k "$HOME/.tart" | awk 'NR == 2 { print $4 }')
(( free_kb / 1024 / 1024 >= 20 )) || fail "Only $((free_kb / 1024 / 1024)) GiB free — need at least 20 GiB"

# A SIGKILL skips the trap, so sweep clones left by earlier runs of this script. Only its own
# run-id shape is matched, which can never be a golden image or another project's clone.
for old in $(tart list --quiet | grep -E "^$PREFIX-[0-9]{8}-[0-9]{6}-[0-9]+\$" || true); do
  [[ "$old" == "$CLONE" ]] && continue
  log "Sweeping stale clone: $old"
  tart stop "$old" >/dev/null 2>&1 || true
  tart delete "$old" >/dev/null 2>&1 || true
done

# --- Build and stage the export ------------------------------------------------

phase=$SECONDS
if (( BUILD )); then
  log "Building the app on the host"
  "$REPO/scripts/build.sh" >"$EXPORT/build.log" 2>&1 || { tail -20 "$EXPORT/build.log" >&2; fail "Build failed"; }
fi
[[ -d "$APP" ]] || fail "No app at $APP (drop --no-build)"
T[build]=$(( SECONDS - phase ))

# Tart's shares don't carry symlinks well, so the app goes in as an archive.
ditto -c -k --keepParent "$APP" "$EXPORT/app.zip"
mkdir -p "$EXPORT/server"
if [[ -d "$SERVER" ]]; then cp -R "$SERVER/." "$EXPORT/server/"; else cp "$SERVER" "$EXPORT/server/"; fi
cp "$REPO/scripts/vm-guest-play.py" "$EXPORT/"
# Settings' "Test connection" (App.TestAgent) and one decision per game, run in the guest against
# the agent by testagent_test.go; the guest has no Go, so the test binary is built here.
(cd "$REPO" && GOOS=darwin GOARCH=arm64 go test -c -o "$EXPORT/system1.test" .) >>"$EXPORT/build.log" 2>&1 \
  || { tail -20 "$EXPORT/build.log" >&2; fail "Building the test binary failed"; }
PLAY_ARGS="--agent-url $AGENT_URL --server-pid-file $GUEST_RESULTS/server.pid"
if [[ -n "$VERIFY" ]]; then
  cp "$VERIFY" "$EXPORT/verify.jsonl"
  PLAY_ARGS+=" --verify '$SHARE/run/verify.jsonl' --verify-every $VERIFY_EVERY"
fi
print -r -- "{\"agent\": \"custom\", \"url\": \"$AGENT_URL\", \"batch\": $BATCH, \"inputRate\": $INPUT_RATE}" \
  > "$EXPORT/settings.json"
print -r -- "$SERVER_CMD" > "$EXPORT/server-cmd"

# A Hugging Face cache is mostly symlinks into blobs/; resolve them. cp -c clones files on APFS,
# so this costs no disk.
DIR_ARGS=(--dir=run:"$EXPORT":ro)
if [[ -n "$MODEL_DIR" ]]; then
  log "Staging the model folder (symlinks resolved, APFS clones)"
  MODEL_STAGE="$STAGE/model"
  mkdir -p "$MODEL_STAGE"
  cp -c -R -L "$MODEL_DIR/." "$MODEL_STAGE/"
  DIR_ARGS+=(--dir=model:"$MODEL_STAGE":ro)
  GUEST_MODEL_DIR="$SHARE/model"
else
  GUEST_MODEL_DIR=""
fi
T[stage]=$(( SECONDS - phase - T[build] ))

# --- Wait for a VM slot, clone and boot -----------------------------------------

phase=$SECONDS
booted=0
until (( booted )); do
  # Slot admission is tart-lease's job now. The loop this replaced counted
  # running VMs itself, which raced: two waiters polling on the same cycle
  # could both see a free slot and both start. tart-lease decides under a
  # lock, and also gates on memory rather than a bare count.
  # See Homelab protocols/tart-lease/PROTOCOL.md
  wait_start=$SECONDS
  if command -v tart-lease >/dev/null; then
    LEASE_ID=$(TART_LEASE_MAX_SLOTS="$MAX_MACOS_VMS" \
      tart-lease acquire --label system1 --pid $$ --timeout "$SLOT_TIMEOUT") \
      || fail "No free VM slot after ${SLOT_TIMEOUT}s. Other projects' VMs are still running; try again later or raise --slot-timeout."
  else
    log "WARNING: tart-lease not on PATH — running without admission control"
  fi
  T[slot_wait]=$(( ${T[slot_wait]:-0} + SECONDS - wait_start ))

  step=$SECONDS
  vm_exists "$CLONE" || { log "Cloning $GOLDEN → $CLONE"; tart clone "$GOLDEN" "$CLONE"; tart set "$CLONE" --memory "$MEMORY_MB"; }
  T[clone]=$(( SECONDS - step ))

  step=$SECONDS
  log "Booting $CLONE (headless, export shared read-only)"
  tart run "$CLONE" --no-graphics --no-audio --no-clipboard "${DIR_ARGS[@]}" >"$EXPORT/tart-run.log" 2>&1 &
  TART_PID=$!
  deadline=$(( SECONDS + BOOT_TIMEOUT_SECS ))
  while true; do
    if tart exec "$CLONE" true >/dev/null 2>&1; then booted=1; break; fi
    if ! kill -0 "$TART_PID" 2>/dev/null; then
      # Most likely another VM took the slot between the check and the boot.
      log "tart run exited before the guest came up: $(tail -1 "$EXPORT/tart-run.log")"
      TART_PID=""
      sleep 15
      break
    fi
    (( SECONDS < deadline )) || fail "Guest did not become reachable within ${BOOT_TIMEOUT_SECS}s"
    sleep 3
  done
done
T[boot]=$(( SECONDS - step ))

# The app needs admin's GUI (Aqua) session, which the golden logs into automatically.
for i in {1..30}; do
  [[ "$(tart exec "$CLONE" stat -f %Su /dev/console 2>/dev/null)" == "admin" ]] && break
  (( i < 30 )) || fail "No GUI login in the guest (console user is not admin)"
  sleep 2
done
log "Guest up in ${T[boot]}s, admin logged in to the GUI"

# --- In the guest ---------------------------------------------------------------

gx() { tart exec "$CLONE" /bin/zsh -lc "$1"; }

phase=$SECONDS
log "Installing the app and the agent server in the guest"
gx "set -e
  rm -rf $GUEST_RESULTS ~/server && mkdir -p $GUEST_RESULTS
  ditto -x -k '$SHARE/run/app.zip' /Applications
  xattr -dr com.apple.quarantine '/Applications/System 1 Arcade.app' 2>/dev/null || true
  cp -R '$SHARE/run/server' ~/server && chmod -R u+x ~/server
  mkdir -p ~/'Library/Application Support/System 1 Arcade'
  cp '$SHARE/run/settings.json' ~/'Library/Application Support/System 1 Arcade/settings.json'"

log "Starting the agent server: $SERVER_CMD"
gx "cd ~/server && export PORT=$PORT MODEL_DIR=${(q)GUEST_MODEL_DIR}
  nohup /bin/zsh -lc \"\$(cat '$SHARE/run/server-cmd')\" > $GUEST_RESULTS/server.log 2>&1 < /dev/null &
  echo \$! > $GUEST_RESULTS/server.pid"

deadline=$(( SECONDS + SERVER_TIMEOUT ))
until [[ "$(gx "curl -s -o /dev/null -w '%{http_code}' -m 2 $AGENT_URL" 2>/dev/null || true)" =~ '^[1-5][0-9][0-9]$' ]]; do
  if ! gx "kill -0 \$(cat $GUEST_RESULTS/server.pid)" >/dev/null 2>&1; then
    gx "tail -20 $GUEST_RESULTS/server.log" >&2 || true
    fail "The agent server exited during startup (log above)"
  fi
  (( SECONDS < deadline )) || fail "The agent did not answer at $AGENT_URL within ${SERVER_TIMEOUT}s"
  sleep 2
done
T[server_start]=$(( SECONDS - phase ))
log "Agent server answering after ${T[server_start]}s"

log "Test connection (App.TestAgent) and one decision per game, from the guest"
if gx "cd $GUEST_RESULTS && SYSTEM1_TEST_AGENT_URL=$AGENT_URL '$SHARE/run/system1.test' -test.run 'TestAgentLive\$' -test.v > testagent.log 2>&1"; then
  gx "grep -E 'Connected|answers in' $GUEST_RESULTS/testagent.log" | sed 's/^ */    /'
else
  gx "tail -20 $GUEST_RESULTS/testagent.log" >&2 || true
  log "Test connection FAILED (see testagent.log)"
fi

phase=$SECONDS
log "Playing games=$GAMES seeds=$SEEDS cap=${CAP}s"
set +e
gx "/usr/bin/python3 '$SHARE/run/vm-guest-play.py' --games ${(q)GAMES} --seeds ${(q)SEEDS} --cap $CAP --results $GUEST_RESULTS $PLAY_ARGS"
play_rc=$?
set -e
T[play]=$(( SECONDS - phase ))

gx "kill \$(cat $GUEST_RESULTS/server.pid) 2>/dev/null; pkill -x System1; true" >/dev/null 2>&1 || true

# --- Results back ------------------------------------------------------------------

mkdir -p "$RESULTS_DIR"
tart exec "$CLONE" /bin/zsh -lc "tar -C $GUEST_RESULTS -cf - ." | tar -x -C "$RESULTS_DIR"
cp "$EXPORT/tart-run.log" "$RESULTS_DIR/" 2>/dev/null || true
[[ -f "$EXPORT/build.log" ]] && cp "$EXPORT/build.log" "$RESULTS_DIR/"
T[total]=$(( SECONDS - T0 ))

timings="{$(for k in build stage slot_wait clone boot server_start play total; do printf '"%s": %s, ' $k ${T[$k]:-0}; done | sed 's/, $//')}"
print -r -- "$timings" > "$RESULTS_DIR/timings.json"

print ""
log "Results: $RESULTS_DIR/results.jsonl"
/usr/bin/python3 - "$RESULTS_DIR/results.jsonl" <<'EOF' | tee "$RESULTS_DIR/summary.txt"
import json, sys
import os
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()] if os.path.exists(sys.argv[1]) else []
if not rows:
    print('no results')
print(f"{'game':<9} {'seed':>5} {'score':>7} {'level':>5} {'lives':>5} {'ended':<6} {'game s':>7} {'wall s':>7} {'tick/s':>6} {'1st ans s':>9} {'ans/s':>7} {'calls':>6} {'srv MB':>7}")
for r in rows:
    if "error" in r:
        print(f"{r['game']:<9} {r['seed']:>5}  ERROR: {r['error']}")
        continue
    print(f"{r['game']:<9} {r['seed']:>5} {r['score']:>7} {r['level']:>5} {r['lives']:>5} {r['ended']:<6} "
          f"{r['game_secs']:>7} {r['wall_secs']:>7} {str(r.get('ticks_per_sec')):>6} {str(r['first_answer_secs']):>9} "
          f"{str(r.get('answers_per_sec', '-')):>7} {str(r.get('model_calls', '-')):>6} {str(r.get('server_rss_mb', '-')):>7}")
by = {}
for r in rows:
    if "error" not in r:
        by.setdefault(r["game"], []).append(r["score"])
for g, s in by.items():
    print(f"{g}: {len(s)} game(s), mean score {sum(s) / len(s):.1f}, best {max(s)}")
d = os.path.dirname(sys.argv[1])
vp = os.path.join(d, "verify.jsonl")
if os.path.exists(vp):
    vs = [json.loads(l) for l in open(vp) if l.strip()]
    for phase in ("during", "after"):
        v = [x for x in vs if x["phase"] == phase]
        if v:
            ms = sorted(x["ms"] for x in v)
            print(f"verify {phase} play: {sum(x['ok'] for x in v)}/{len(v)} agree, {sum(x['exact'] for x in v)} byte-identical, "
                  f"ms median {ms[len(ms) // 2]} p90 {ms[int(len(ms) * 0.9)]} max {ms[-1]}")
mp = os.path.join(d, "monitor.jsonl")
if os.path.exists(mp):
    rss = [m["server_rss_mb"] for m in map(json.loads, open(mp)) if m.get("server_rss_mb")]
    if rss:
        print(f"server RSS MB: first {rss[0]}, min {min(rss)}, max {max(rss)}, last {rss[-1]} ({len(rss)} samples)")
import re
pat = re.compile(r"panic|fatal|goroutine \d+ \[|SIGSEGV|NaN|\b[45]\d\d:|http: |agent error|error", re.I)
for name in sorted(os.listdir(d)):
    if name.endswith(".log") and name not in ("build.log", "tart-run.log"):
        hits = [l.rstrip() for l in open(os.path.join(d, name), errors="replace") if pat.search(l)]
        if hits:
            print(f"{name}: {len(hits)} suspicious line(s), first: {hits[0][:200]}")
EOF
log "Timings (s): $timings"
exit $play_rc
