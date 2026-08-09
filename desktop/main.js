"use strict";

const { app, BrowserWindow, ipcMain, session, shell } = require("electron");
const path = require("node:path");
const fs = require("node:fs");
const { spawn } = require("node:child_process");

if (process.platform === "linux") {
    app.commandLine.appendSwitch("no-sandbox");
}

const WINDOW_WIDTH = 480;
const WINDOW_HEIGHT = 680;
const PATH_BYTES_MAX = 4096;
const BUCKET_BYTES_MAX = 256;
const STATUS_BYTES_MAX = 512;
const AUTH_REQUEST_BYTES_MAX = 2_048;
const AUTH_RESPONSE_BYTES_MAX = 16_384;
const AUTH_REQUEST_TIMEOUT_MS = 10_000;
const MOUNT_CHILDREN_MAX = 1;

function assert(condition, message) {
    if (condition) {
        return;
    }
    throw new Error(message);
}

function assert_string_bound(value, bytes_max, label) {
    assert(typeof value === "string", `${label} must be a string`);
    assert(Buffer.byteLength(value, "utf8") <= bytes_max, `${label} exceeds ${bytes_max} bytes`);
}

/** @type {BrowserWindow | null} */
let window_main = null;

/** @type {import("node:child_process").ChildProcess | null} */
let mount_child = null;

/** @type {"idle" | "mounted" | "error"} */
let mount_state = "idle";

/** @type {string} */
let status_text = "Idle";

function set_status(state, text) {
    assert(state === "idle" || state === "mounted" || state === "error", "bad mount state");
    assert_string_bound(text, STATUS_BYTES_MAX, "status");
    mount_state = state;
    status_text = text;
    if (window_main !== null && !window_main.isDestroyed()) {
        window_main.webContents.send("infinity_storage:status", {
            state: mount_state,
            text: status_text,
        });
    }
}

function create_window() {
    assert(window_main === null, "only one window allowed");

    const preload_path = path.join(__dirname, "preload.js");
    const index_path = path.join(__dirname, "renderer", "index.html");
    assert(fs.existsSync(preload_path), "preload.js missing");
    assert(fs.existsSync(index_path), "renderer/index.html missing");

    window_main = new BrowserWindow({
        width: WINDOW_WIDTH,
        height: WINDOW_HEIGHT,
        resizable: false,
        maximizable: false,
        title: "Infinity Storage",
        show: false,
        webPreferences: {
            preload: preload_path,
            contextIsolation: true,
            nodeIntegration: false,
            sandbox: true,
        },
    });

    window_main.once("ready-to-show", () => {
        assert(window_main !== null, "window gone before show");
        window_main.show();
    });

    window_main.on("closed", () => {
        window_main = null;
    });

    void window_main.loadFile(index_path);
}

function resolve_api_url() {
    const value = process.env.INFINITY_STORAGE_API_URL ?? "http://127.0.0.1:3005";
    assert_string_bound(value, PATH_BYTES_MAX, "INFINITY_STORAGE_API_URL");
    const url = new URL(value);
    assert(url.protocol === "http:" || url.protocol === "https:", "invalid API URL protocol");
    assert(url.username.length === 0, "API URL must not contain credentials");
    assert(url.password.length === 0, "API URL must not contain credentials");
    return url;
}

async function auth_request(pathname, method, body) {
    assert(typeof pathname === "string", "auth pathname required");
    assert(method === "GET" || method === "POST", "invalid auth method");
    const url = new URL(pathname, resolve_api_url());
    const options = {
        credentials: "include",
        headers: {
            "content-type": "application/json",
            origin: url.origin,
        },
        method,
        signal: AbortSignal.timeout(AUTH_REQUEST_TIMEOUT_MS),
    };
    if (body !== undefined) {
        const request_body = JSON.stringify(body);
        assert(
            Buffer.byteLength(request_body, "utf8") <= AUTH_REQUEST_BYTES_MAX,
            "auth request too large",
        );
        options.body = request_body;
    }
    const response = await session.defaultSession.fetch(url.toString(), options);
    const text = await response.text();
    assert(
        Buffer.byteLength(text, "utf8") <= AUTH_RESPONSE_BYTES_MAX,
        "auth response too large",
    );
    const data = text.length === 0 ? null : JSON.parse(text);
    if (response.ok === false) {
        const message = typeof data?.message === "string"
            ? data.message
            : `auth failed (${response.status})`;
        throw new Error(message.slice(0, STATUS_BYTES_MAX));
    }
    return data;
}

function resolve_mount_bin() {
    const env_bin = process.env.INFINITY_STORAGE_MOUNT_BIN;
    if (typeof env_bin === "string" && env_bin.length > 0) {
        assert_string_bound(env_bin, PATH_BYTES_MAX, "INFINITY_STORAGE_MOUNT_BIN");
        return env_bin;
    }
    const goos = process.platform === "win32" ? "windows" : "linux";
    const name = goos === "windows" ? "infinity-storage-mount.exe" : "infinity-storage-mount";
    return path.join(__dirname, "..", name);
}

function resolve_live_relay() {
    const url = process.env.INFINITY_STORAGE_LIVE_RELAY_URL ?? "";
    const library_id = process.env.INFINITY_STORAGE_LIBRARY_ID ?? "";
    const token_file = process.env.INFINITY_STORAGE_RELAY_TOKEN_FILE ?? "";
    const configured = [url, library_id, token_file].filter((value) => value.length > 0).length;
    if (configured === 0) {
        return null;
    }
    assert(configured === 3, "live relay requires URL, library ID, and token file");
    assert_string_bound(url, PATH_BYTES_MAX, "INFINITY_STORAGE_LIVE_RELAY_URL");
    assert_string_bound(library_id, BUCKET_BYTES_MAX, "INFINITY_STORAGE_LIBRARY_ID");
    assert_string_bound(token_file, PATH_BYTES_MAX, "INFINITY_STORAGE_RELAY_TOKEN_FILE");
    const parsed = new URL(url);
    assert(parsed.protocol === "http:" || parsed.protocol === "https:", "invalid live relay URL protocol");
    assert(parsed.username.length === 0, "live relay URL must not contain credentials");
    assert(parsed.password.length === 0, "live relay URL must not contain credentials");
    assert(fs.existsSync(token_file), "live relay token file not found");
    return { url, library_id, token_file };
}

function resolve_proxy_bin() {
    const env_bin = process.env.INFINITY_STORAGE_PROXY_BIN;
    if (typeof env_bin === "string" && env_bin.length > 0) {
        assert_string_bound(env_bin, PATH_BYTES_MAX, "INFINITY_STORAGE_PROXY_BIN");
        return env_bin;
    }
    const goos = process.platform === "win32" ? "windows" : "linux";
    const name = goos === "windows" ? "stream_proxy.exe" : "stream_proxy";
    return path.join(__dirname, "..", "stream_proxy", "zig-out", "bin", name);
}

function stop_mount_child() {
    if (mount_child === null) {
        return;
    }
    const child = mount_child;
    mount_child = null;
    if (child.exitCode === null && child.signalCode === null) {
        child.kill("SIGTERM");
    }
}

function start_mount(options) {
    assert(typeof options === "object" && options !== null, "options required");
    assert_string_bound(options.mount_path, PATH_BYTES_MAX, "mount_path");
    assert_string_bound(options.bucket, BUCKET_BYTES_MAX, "bucket");
    assert(options.mount_path.length > 0, "mount_path empty");
    assert(options.bucket.length > 0, "bucket empty");
    assert(mount_child === null, "mount already running");
    assert(MOUNT_CHILDREN_MAX === 1, "MOUNT_CHILDREN_MAX must be 1");

    const mount_bin = resolve_mount_bin();
    const proxy_bin = resolve_proxy_bin();
    assert(fs.existsSync(mount_bin), `infinity-storage-mount not found: ${mount_bin}`);
    assert(fs.existsSync(proxy_bin), `stream_proxy not found: ${proxy_bin}`);

    const args = [
        "--mount", options.mount_path,
        "--bucket", options.bucket,
        "--proxy-bin", proxy_bin,
    ];
    const relay = resolve_live_relay();
    if (relay !== null) {
        args.push(
            "--live-relay-url", relay.url,
            "--library-id", relay.library_id,
            "--relay-token-file", relay.token_file,
        );
    }

    const child = spawn(mount_bin, args, {
        stdio: ["ignore", "pipe", "pipe"],
        windowsHide: true,
    });
    mount_child = child;

    let stderr_tail = "";
    child.stderr.on("data", (chunk) => {
        const text = String(chunk);
        stderr_tail = (stderr_tail + text).slice(-STATUS_BYTES_MAX);
    });

    child.on("error", (err) => {
        if (mount_child === child) {
            mount_child = null;
        }
        set_status("error", `spawn failed: ${err.message}`.slice(0, STATUS_BYTES_MAX));
    });

    child.on("exit", (code, signal) => {
        if (mount_child === child) {
            mount_child = null;
        }
        if (mount_state === "mounted") {
            const detail = signal !== null
                ? `signal ${signal}`
                : `exit ${code}`;
            const message = `Mount stopped (${detail})`.slice(0, STATUS_BYTES_MAX);
            set_status("idle", message);
        } else if (code !== 0 && code !== null) {
            const message = (stderr_tail.length > 0 ? stderr_tail : `mount exit ${code}`)
                .slice(0, STATUS_BYTES_MAX);
            set_status("error", message);
        }
    });

    set_status("mounted", `Mounted ${options.mount_path}`);
}

function open_mount_path(mount_path) {
    assert_string_bound(mount_path, PATH_BYTES_MAX, "mount_path");
    assert(mount_path.length > 0, "mount_path empty");
    return shell.openPath(mount_path);
}

function register_auth_ipc() {
    ipcMain.handle("infinity_storage:auth_session", () => {
        return auth_request("/api/auth/get-session", "GET");
    });
    ipcMain.handle("infinity_storage:auth_sign_up", (_event, credentials) => {
        return auth_request("/api/auth/sign-up/email", "POST", credentials);
    });
    ipcMain.handle("infinity_storage:auth_sign_in", (_event, credentials) => {
        return auth_request("/api/auth/sign-in/email", "POST", credentials);
    });
    ipcMain.handle("infinity_storage:auth_sign_out", () => {
        return auth_request("/api/auth/sign-out", "POST", {});
    });
    ipcMain.handle("infinity_storage:auth_reset", (_event, email) => {
        const redirectTo = new URL("/reset-password", resolve_api_url()).toString();
        return auth_request("/api/auth/request-password-reset", "POST", { email, redirectTo });
    });
}

function register_mount_ipc() {
    ipcMain.handle("infinity_storage:get_status", () => {
        return { state: mount_state, text: status_text };
    });

    ipcMain.handle("infinity_storage:mount", (_event, options) => {
        try {
            start_mount(options);
            return { ok: true, state: mount_state, text: status_text };
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            set_status("error", message.slice(0, STATUS_BYTES_MAX));
            return { ok: false, state: mount_state, text: status_text };
        }
    });

    ipcMain.handle("infinity_storage:unmount", () => {
        stop_mount_child();
        set_status("idle", "Idle");
        return { ok: true, state: mount_state, text: status_text };
    });

    ipcMain.handle("infinity_storage:open_folder", async (_event, mount_path) => {
        try {
            const error_text = await open_mount_path(mount_path);
            if (error_text.length > 0) {
                set_status("error", error_text.slice(0, STATUS_BYTES_MAX));
                return { ok: false, state: mount_state, text: status_text };
            }
            return { ok: true, state: mount_state, text: status_text };
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            set_status("error", message.slice(0, STATUS_BYTES_MAX));
            return { ok: false, state: mount_state, text: status_text };
        }
    });
}

app.whenReady().then(() => {
    register_auth_ipc();
    register_mount_ipc();
    create_window();
});

app.on("window-all-closed", () => {
    stop_mount_child();
    app.quit();
});

app.on("before-quit", () => {
    stop_mount_child();
});
