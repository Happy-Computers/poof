#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${INFINITY_STORAGE_ENV_FILE:-$repository_root/.env}"

if [[ ! -f "$env_file" ]]; then
    printf 'missing environment file: %s\n' "$env_file" >&2
    exit 1
fi

set -a
source "$env_file"
set +a

if [[ -z "${INFINITY_STORAGE_API_URL:-}" ]]; then
    export INFINITY_STORAGE_API_URL="${BETTER_AUTH_URL:?BETTER_AUTH_URL missing from $env_file}"
fi

for command in go npm zig; do
    if ! command -v "$command" >/dev/null 2>&1; then
        printf 'required command not found: %s\n' "$command" >&2
        exit 1
    fi
done

api_runner="$repository_root/api/node_modules/.bin/tsx"
if [[ ! -x "$api_runner" ]]; then
    printf 'API dependencies missing; run npm install in api\n' >&2
    exit 1
fi

mkdir -p "$repository_root/.logs"

(
    cd "$repository_root/stream_proxy"
    zig build
)
go build -o "$repository_root/infinity-storage-mount" ./cmd/infinity-storage-mount

(
    cd "$repository_root/electron-desktop"
    npm exec vite build
)

(
    cd "$repository_root/api"
    exec "$api_runner" --env-file="$env_file" src/server.ts
) >"$repository_root/.logs/api.stdout.log" 2>"$repository_root/.logs/api.stderr.log" &
api_pid=$!

cleanup() {
    kill "$api_pid" 2>/dev/null || true
    wait "$api_pid" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

sleep 2
if ! kill -0 "$api_pid" 2>/dev/null; then
    cat "$repository_root/.logs/api.stderr.log" >&2
    exit 1
fi

cd "$repository_root/electron-desktop"
npm run start
