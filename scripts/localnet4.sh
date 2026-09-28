#!/usr/bin/env bash
# Four-validator local chain. Home defaults to .localnet4 under the repo root.
# Usage: scripts/localnet4.sh [start|stop|reset|smoke]
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
bin="${BIN:-$root/build/cosmos-orderbookd}"
home="${HOME_DIR:-$root/.localnet4}"
chain_id="${CHAIN_ID:-orderbook-1}"
nodes=4

stop_nodes() {
  if [[ -f "$home/pids" ]]; then
    while read -r pid; do
      if [[ -n "$pid" ]]; then
        kill "$pid" 2>/dev/null || true
      fi
    done <"$home/pids"
    rm -f "$home/pids"
  fi
  # Give the ports a moment to close.
  sleep 0.3
}

rpc_port() { echo $((26657 + $1 * 10)); }
p2p_port() { echo $((26656 + $1 * 10)); }
proxy_port() { echo $((26658 + $1 * 10)); }
grpc_port() { echo $((9090 + $1 * 10)); }
api_port() { echo $((1317 + $1 * 10)); }

configure_node() {
  local i="$1"
  local peers="$2"
  local node="$home/node$i"
  python3 - "$node/config/config.toml" "$node/config/app.toml" "$i" "$peers" <<'PY'
import sys
config_path, app_path, index, peers = sys.argv[1:]
i = int(index)
p2p = 26656 + i * 10
rpc = 26657 + i * 10
proxy = 26658 + i * 10
# Stay off 9090, which Prometheus often uses, and give each node its own port.
grpc = 29290 + i * 10
api = 29340 + i * 10
grpc_web = 29380 + i * 10

def load(path):
    with open(path) as f:
        return f.read().splitlines()

def dump(path, lines):
    with open(path, "w") as f:
        f.write("\n".join(lines) + "\n")

def set_key(lines, section, key, value):
    current = ""
    found = False
    out = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]") and not stripped.startswith("[["):
            current = stripped[1:-1]
        if current == section and not stripped.startswith("#") and stripped.startswith(key + " ") or (
            current == section and not stripped.startswith("#") and stripped.startswith(key + "=")
        ):
            indent = line[: len(line) - len(line.lstrip())]
            out.append(f"{indent}{key} = {value}")
            found = True
            continue
        out.append(line)
    if not found:
        raise SystemExit(f"missing [{section}] {key} in config")
    return out

cfg = load(config_path)
cfg = set_key(cfg, "", "moniker", f'"validator-{i}"')
cfg = set_key(cfg, "", "proxy_app", f'"tcp://127.0.0.1:{proxy}"')
cfg = set_key(cfg, "rpc", "laddr", f'"tcp://127.0.0.1:{rpc}"')
cfg = set_key(cfg, "rpc", "pprof_laddr", '""')
cfg = set_key(cfg, "p2p", "laddr", f'"tcp://127.0.0.1:{p2p}"')
cfg = set_key(cfg, "p2p", "persistent_peers", f'"{peers}"')
cfg = set_key(cfg, "p2p", "seeds", '""')
cfg = set_key(cfg, "p2p", "addr_book_strict", "false")
cfg = set_key(cfg, "p2p", "allow_duplicate_ip", "true")
cfg = set_key(cfg, "p2p", "pex", "false")
cfg = set_key(cfg, "consensus", "timeout_commit", '"1s"')
dump(config_path, cfg)

app = load(app_path)
app = set_key(app, "", "minimum-gas-prices", '"0stake"')
app = set_key(app, "api", "enable", "true")
app = set_key(app, "api", "address", f'"tcp://127.0.0.1:{api}"')
app = set_key(app, "grpc", "enable", "true")
app = set_key(app, "grpc", "address", f'"127.0.0.1:{grpc}"')
try:
    app = set_key(app, "grpc-web", "address", f'"127.0.0.1:{grpc_web}"')
except SystemExit:
    pass
dump(app_path, app)
PY
}

start_nodes() {
  if [[ ! -x "$bin" ]]; then
    mkdir -p "$(dirname "$bin")"
    (cd "$root" && go build -o "$bin" ./cmd/cosmos-orderbookd)
  fi
  stop_nodes
  rm -rf "$home"
  mkdir -p "$home"

  for i in $(seq 0 $((nodes - 1))); do
    "$bin" init "validator-$i" --chain-id "$chain_id" --home "$home/node$i" >/dev/null 2>&1
    "$bin" keys add "validator" --keyring-backend test --home "$home/node$i" >/dev/null 2>&1
  done
  "$bin" keys add alice --keyring-backend test --home "$home/node0" >/dev/null 2>&1
  "$bin" keys add bob --keyring-backend test --home "$home/node0" >/dev/null 2>&1
  "$bin" keys add submitter --keyring-backend test --home "$home/node0" >/dev/null 2>&1

  for i in $(seq 0 $((nodes - 1))); do
    addr="$("$bin" keys show validator -a --keyring-backend test --home "$home/node$i")"
    "$bin" genesis add-genesis-account "$addr" 100000000000stake --home "$home/node0" >/dev/null
  done
  "$bin" genesis add-genesis-account alice 100000000000stake,100000000base --keyring-backend test --home "$home/node0" >/dev/null
  "$bin" genesis add-genesis-account bob 100000000000stake,100000000quote --keyring-backend test --home "$home/node0" >/dev/null
  "$bin" genesis add-genesis-account submitter 100000000000stake --keyring-backend test --home "$home/node0" >/dev/null

  for i in $(seq 1 $((nodes - 1))); do
    cp "$home/node0/config/genesis.json" "$home/node$i/config/genesis.json"
  done
  for i in $(seq 0 $((nodes - 1))); do
    "$bin" genesis gentx validator 100000000000stake --chain-id "$chain_id" --keyring-backend test --home "$home/node$i" >/dev/null 2>&1
  done
  for i in $(seq 1 $((nodes - 1))); do
    cp "$home/node$i/config/gentx/"* "$home/node0/config/gentx/"
  done
  "$bin" genesis collect-gentxs --home "$home/node0" >/dev/null 2>&1

  submitter="$("$bin" keys show submitter -a --keyring-backend test --home "$home/node0")"
  python3 - "$home/node0/config/genesis.json" "$submitter" <<'PY'
import json, sys
path, addr = sys.argv[1], sys.argv[2]
with open(path) as f:
    doc = json.load(f)
doc["app_state"]["batch"]["submitter"] = addr
with open(path, "w") as f:
    json.dump(doc, f, indent=2)
    f.write("\n")
PY

  ids=()
  for i in $(seq 0 $((nodes - 1))); do
    ids[$i]="$("$bin" comet show-node-id --home "$home/node$i")"
  done
  for i in $(seq 1 $((nodes - 1))); do
    cp "$home/node0/config/genesis.json" "$home/node$i/config/genesis.json"
  done
  for i in $(seq 0 $((nodes - 1))); do
    peers=""
    for j in $(seq 0 $((nodes - 1))); do
      if [[ "$j" == "$i" ]]; then
        continue
      fi
      if [[ -n "$peers" ]]; then
        peers+=","
      fi
      peers+="${ids[$j]}@127.0.0.1:$(p2p_port "$j")"
    done
    configure_node "$i" "$peers"
  done

  : >"$home/pids"
  for i in $(seq 0 $((nodes - 1))); do
    "$bin" start --home "$home/node$i" --minimum-gas-prices 0stake >"$home/node$i.log" 2>&1 &
    echo $! >>"$home/pids"
  done
  echo "chain $chain_id home $home"
  echo "rpc node0 tcp://127.0.0.1:$(rpc_port 0)"
  echo "batch submitter $submitter"
}

wait_for_height() {
  local min_height="$1"
  python3 - "$min_height" "$(rpc_port 0)" "$(rpc_port 1)" "$(rpc_port 2)" "$(rpc_port 3)" <<'PY'
import json, sys, time, urllib.request
min_height = int(sys.argv[1])
ports = [int(p) for p in sys.argv[2:]]

def height(port):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/status", timeout=2) as res:
        doc = json.load(res)
    return int(doc["result"]["sync_info"]["latest_block_height"])

for _ in range(90):
    try:
        heights = [height(port) for port in ports]
    except Exception:
        time.sleep(0.5)
        continue
    if min(heights) >= min_height and len(set(heights)) == 1:
        print(heights[0])
        sys.exit(0)
    time.sleep(0.5)
sys.exit("validators did not reach a common height")
PY
}

tx() {
  local from="$1"
  shift
  local node="tcp://127.0.0.1:$(rpc_port 0)"
  local out
  out="$("$bin" tx exchange "$@" \
    --from "$from" \
    --chain-id "$chain_id" \
    --keyring-backend test \
    --home "$home/node0" \
    --node "$node" \
    --fees 1stake \
    --gas 400000 \
    --broadcast-mode sync \
    -o json \
    -y)"
  local hash code
  hash="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["txhash"])' <<<"$out")"
  code="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("code", 0))' <<<"$out")"
  if [[ "$code" != "0" || -z "$hash" ]]; then
    echo "$out" >&2
    return 1
  fi
  for _ in $(seq 1 40); do
    local qout
    if qout="$("$bin" query tx "$hash" --node "$node" --home "$home/node0" -o json 2>/dev/null)"; then
      local qcode
      qcode="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("code", 1))' <<<"$qout")"
      if [[ "$qcode" != "0" ]]; then
        echo "$qout" >&2
        echo "tx $hash committed with code $qcode" >&2
        return 1
      fi
      return 0
    fi
    sleep 0.4
  done
  echo "tx $hash was not committed" >&2
  return 1
}

query_all() {
  local name="$1"
  shift
  local first=""
  for i in $(seq 0 $((nodes - 1))); do
    local out
    if ! out="$("$bin" "$@" --node "tcp://127.0.0.1:$(rpc_port "$i")" --home "$home/node0" -o json 2>/dev/null)"; then
      echo "query $name failed on node$i" >&2
      echo "$out" >&2
      return 1
    fi
    if [[ -z "$first" ]]; then
      first="$out"
    elif [[ "$out" != "$first" ]]; then
      echo "disagreement on $name" >&2
      echo "node0: $first" >&2
      echo "node$i: $out" >&2
      return 1
    fi
  done
  printf '%s\n' "$first"
}

query_not_found() {
  local name="$1"
  shift
  for i in $(seq 0 $((nodes - 1))); do
    local out
    if out="$("$bin" "$@" --node "tcp://127.0.0.1:$(rpc_port "$i")" --home "$home/node0" -o json 2>&1)"; then
      echo "$name succeeded on node$i; expected not found" >&2
      echo "$out" >&2
      return 1
    fi
    if [[ "$out" != *"not found"* ]]; then
      echo "$name on node$i was not the expected not-found result" >&2
      echo "$out" >&2
      return 1
    fi
  done
}

smoke() {
  start_nodes
  trap stop_nodes EXIT
  wait_for_height 2 >/dev/null
  tx alice deposit 1000base
  tx bob deposit 10000quote
  tx alice place-limit-order 1 sell 10 100 1
  tx bob place-limit-order 1 buy 10 100 1
  local trades=""
  for _ in $(seq 1 60); do
    trades="$("$bin" query exchange trades 1 --node "tcp://127.0.0.1:$(rpc_port 0)" --home "$home/node0" -o json 2>/dev/null || true)"
    if [[ "$trades" == *'"sequence"'* ]]; then
      break
    fi
    sleep 0.5
  done
  if [[ "$trades" != *'"sequence"'* ]]; then
    echo "trade was not included" >&2
    return 1
  fi
  local height
  height="$(wait_for_height 2)"
  local alice bob
  alice="$("$bin" keys show alice -a --keyring-backend test --home "$home/node0")"
  bob="$("$bin" keys show bob -a --keyring-backend test --home "$home/node0")"
  query_all revision query exchange revision >/dev/null
  query_all "alice balances" query exchange balances "$alice" >/dev/null
  query_all "bob balances" query exchange balances "$bob" >/dev/null
  query_all trades query exchange trades 1 >/dev/null
  query_all orderbook query exchange orderbook 1 >/dev/null
  query_not_found "latest batch" query batch latest
  echo "agreed at height $height"
  echo "batch query: not found on every node"
}

case "${1:-start}" in
  start)
    start_nodes
    trap stop_nodes EXIT INT TERM
    wait
    ;;
  stop)
    stop_nodes
    ;;
  reset)
    stop_nodes
    rm -rf "$home"
    ;;
  smoke)
    smoke
    ;;
  *)
    echo "usage: $0 [start|stop|reset|smoke]" >&2
    exit 1
    ;;
esac
