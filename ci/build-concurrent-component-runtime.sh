#!/usr/bin/env bash
set -euo pipefail

wasmtime=${1:?usage: ci/build-concurrent-component-runtime.sh /path/to/wasmtime-v46}
repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo "the checked-in patched archive is currently built for Linux x86_64" >&2
  exit 1
fi

git -C "$wasmtime" apply --check "$repo/ci/patches/wasmtime-v46-concurrent-component-api.patch"
git -C "$wasmtime" apply "$repo/ci/patches/wasmtime-v46-concurrent-component-api.patch"
(
  cd "$wasmtime"
  CARGO_PROFILE_RELEASE_LTO=true \
  RUSTFLAGS='-C force-unwind-tables' \
    cargo build --release -p wasmtime-c-api
)
cp "$wasmtime/target/release/libwasmtime.a" "$repo/build/linux-x86_64/libwasmtime.a"
strip --strip-debug "$repo/build/linux-x86_64/libwasmtime.a"
cp "$wasmtime/crates/c-api/include/wasmtime/component/func.h" "$repo/build/include/wasmtime/component/func.h"
cp "$wasmtime/crates/c-api/include/wasmtime/component/linker.h" "$repo/build/include/wasmtime/component/linker.h"
