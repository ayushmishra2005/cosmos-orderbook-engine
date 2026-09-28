#!/usr/bin/env bash
# Regenerate the tracked gogo/cosmos protobuf files.
#
# protoc: 29+ (this tree was checked with libprotoc 36.1)
# protoc-gen-gocosmos: github.com/cosmos/gogoproto/protoc-gen-gocosmos v1.7.2
#   plugins=grpc emits the gRPC services. protoc-gen-go and protoc-gen-go-grpc
#   are not used.
# Includes: proto/, gogoproto, cosmos-sdk/proto, cosmos-proto.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

check=0
if [[ "${1:-}" == "--check" ]]; then
  check=1
fi

missing=0
if ! command -v protoc >/dev/null 2>&1; then
  echo "missing protoc (expected libprotoc 29+; checked with 36.1)" >&2
  missing=1
fi
if ! command -v protoc-gen-gocosmos >/dev/null 2>&1; then
  echo "missing protoc-gen-gocosmos v1.7.2" >&2
  echo "install: GOBIN=\"$root/.tools\" go install github.com/cosmos/gogoproto/protoc-gen-gocosmos@v1.7.2" >&2
  echo "then:    PATH=\"$root/.tools:\$PATH\" make proto-gen" >&2
  missing=1
fi
if [[ "$missing" -ne 0 ]]; then
  exit 1
fi

moddir() {
  go list -m -f '{{.Dir}}' "$1"
}

gogo="$(moddir github.com/cosmos/gogoproto)"
sdk="$(moddir github.com/cosmos/cosmos-sdk)/proto"
cosmosproto="$(moddir github.com/cosmos/cosmos-proto)/proto"

out="."
if [[ "$check" -eq 1 ]]; then
  out="$(mktemp -d)"
  trap 'rm -rf "$out"' EXIT
else
  out="$(mktemp -d)"
  trap 'rm -rf "$out"' EXIT
fi

while IFS= read -r dir; do
  protoc \
    -I proto \
    -I "$gogo" \
    -I "$sdk" \
    -I "$cosmosproto" \
    --gocosmos_out="plugins=grpc:${out}" \
    $(find "$dir" -maxdepth 1 -name '*.proto' | sort)
done < <(find proto -name '*.proto' -exec dirname {} \; | sort -u)

prefix="github.com/ayushmishra2005/cosmos-orderbook-engine/"
if [[ "$check" -eq 0 ]]; then
  while IFS= read -r generated; do
    rel="${generated#"$out"/}"
    rel="${rel#"$prefix"}"
    mkdir -p "$(dirname "$rel")"
    cp "$generated" "$rel"
  done < <(find "$out" -name '*.pb.go' | sort)
  exit 0
fi

fail=0
while IFS= read -r generated; do
  rel="${generated#"$out"/}"
  rel="${rel#"$prefix"}"
  if [[ ! -f "$rel" ]]; then
    echo "generated file is not tracked: $rel" >&2
    fail=1
    continue
  fi
  if ! cmp -s "$generated" "$rel"; then
    echo "protobuf drift: $rel" >&2
    diff -u "$rel" "$generated" | head -n 80 >&2 || true
    fail=1
  fi
done < <(find "$out" -name '*.pb.go' | sort)

if [[ "$fail" -ne 0 ]]; then
  echo "make proto-check failed; tracked pb.go does not match protoc-gen-gocosmos v1.7.2" >&2
  exit 1
fi
echo "protobuf generation matches tracked files"
