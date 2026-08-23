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

for command in curl go lsof npm zig; do
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

api_healthy() {
    [[ "$(curl --silent --max-time 2 "$INFINITY_STORAGE_API_URL/desktop/health" 2>/dev/null)" == '{"version":2}' ]]
}

stop_stale_api() {
    local port="${PORT:-3005}"
    local pid
    while read -r pid; do
        [[ -z "$pid" ]] && continue
        local process_cwd
        local process_command
        process_cwd="$(readlink -f "/proc/$pid/cwd" 2>/dev/null || true)"
        process_command="$(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true)"
        if [[ "$process_cwd" != "$repository_root/api" || "$process_command" != *"src/server.ts"* ]]; then
            printf 'port %s is owned by non-Poof process %s\n' "$port" "$pid" >&2
            exit 1
        fi
        kill "$pid"
    done < <(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null || true)
    sleep 0.5
}

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

api_pid=""
if ! api_healthy; then
    stop_stale_api
    (
        cd "$repository_root/api"
        exec "$api_runner" --env-file="$env_file" src/server.ts
    ) >"$repository_root/.logs/api.stdout.log" 2>"$repository_root/.logs/api.stderr.log" &
    api_pid=$!

    for _ in $(seq 1 20); do
        if api_healthy; then
            break
        fi
        if ! kill -0 "$api_pid" 2>/dev/null; then
            cat "$repository_root/.logs/api.stderr.log" >&2
            exit 1
        fi
        sleep 0.25
    done
    if ! api_healthy; then
        printf 'API did not become healthy at %s\n' "$INFINITY_STORAGE_API_URL" >&2
        exit 1
    fi
fi

cleanup() {
    if [[ -n "$api_pid" ]]; then
        kill "$api_pid" 2>/dev/null || true
        wait "$api_pid" 2>/dev/null || true
    fi
}
trap cleanup EXIT INT TERM

cd "$repository_root/electron-desktop"
npm run start
