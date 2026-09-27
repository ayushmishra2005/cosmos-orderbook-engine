#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
bin="${BIN:-$root/build/cosmos-orderbookd}"
home="${HOME_DIR:-$root/.localnet}"
chain_id="${CHAIN_ID:-orderbook-1}"

if [[ ! -x "$bin" ]]; then
  mkdir -p "$(dirname "$bin")"
  (cd "$root" && go build -o "$bin" ./cmd/cosmos-orderbookd)
fi

rm -rf "$home"
"$bin" init localnet --chain-id "$chain_id" --home "$home" >/dev/null

"$bin" keys add validator --keyring-backend test --home "$home" >/dev/null
"$bin" keys add alice --keyring-backend test --home "$home" >/dev/null
"$bin" keys add bob --keyring-backend test --home "$home" >/dev/null

"$bin" genesis add-genesis-account validator 100000000000stake --keyring-backend test --home "$home"
"$bin" genesis add-genesis-account alice 100000000000stake,100000000base --keyring-backend test --home "$home"
"$bin" genesis add-genesis-account bob 100000000000stake,100000000quote --keyring-backend test --home "$home"
"$bin" genesis gentx validator 100000000000stake --chain-id "$chain_id" --keyring-backend test --home "$home"
"$bin" genesis collect-gentxs --home "$home" >/dev/null

echo "starting $chain_id from $home"
exec "$bin" start --home "$home" --minimum-gas-prices 0stake
