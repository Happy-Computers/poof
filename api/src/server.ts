import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { toNodeHandler } from "better-auth/node";
import { auth, database_pool } from "./auth.js";
import { load_config } from "./config.js";

const SHUTDOWN_TIMEOUT_MS = 5_000;
const MOUNT_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9 _.-]{0,99}$/;
const config = load_config(process.env);
const auth_handler = toNodeHandler(auth);

function json_response(
    response: import("node:http").ServerResponse,
    status: number,
    body: unknown,
): void {
    response.writeHead(status, { "content-type": "application/json; charset=utf-8" });
    response.end(JSON.stringify(body));
}

function valid_mount_name(name: string): boolean {
    return MOUNT_NAME_PATTERN.test(name);
}

function request_headers(request: import("node:http").IncomingMessage): Headers {
    const headers = new Headers();
    for (const [key, value] of Object.entries(request.headers)) {
        if (typeof value === "string") headers.set(key, value);
    }
    return headers;
}

async function request_user_id(request: import("node:http").IncomingMessage): Promise<string | null> {
    const session = await auth.api.getSession({ headers: request_headers(request) }).catch(() => null);
    return session?.user.id ?? null;
}

async function request_body(request: import("node:http").IncomingMessage): Promise<unknown> {
    let body = "";
    for await (const chunk of request) {
        body += chunk;
        if (Buffer.byteLength(body, "utf8") > 4_096) throw new Error("request body too large");
    }
    return JSON.parse(body);
}

async function handle_library_authorization(
    request: import("node:http").IncomingMessage,
    response: import("node:http").ServerResponse,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", config.auth_url);
    const match = /^\/v1\/libraries\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/authorize$/i.exec(url.pathname);
    if (match === null) return false;
    if (request.method !== "GET") {
        json_response(response, 405, { error: "method not allowed" });
        return true;
    }
    const user_id = await request_user_id(request);
    if (user_id === null) {
        json_response(response, 401, { error: "unauthorized" });
        return true;
    }
    const result = await database_pool.query(
        "select 1 from infinity_storage.projects where id = $1 and user_id = $2",
        [match[1], user_id],
    );
    if (result.rowCount !== 1) {
        json_response(response, 403, { error: "forbidden" });
        return true;
    }
    response.writeHead(204).end();
    return true;
}

async function handle_mounts(
    request: import("node:http").IncomingMessage,
    response: import("node:http").ServerResponse,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", config.auth_url);
    const path_match = /^\/v1\/mounts(?:\/([^/]+))?$/.exec(url.pathname);
    if (path_match === null) return false;

    const user_id = await request_user_id(request);
    if (user_id === null) {
        json_response(response, 401, { error: "unauthorized" });
        return true;
    }

    if (path_match[1] !== undefined) {
        if (request.method === "DELETE") {
            const result = await database_pool.query(
                "delete from infinity_storage.mounts where id = $1 and user_id = $2",
                [path_match[1], user_id],
            );
            if (result.rowCount !== 1) {
                json_response(response, 404, { error: "mount not found" });
                return true;
            }
            response.writeHead(204).end();
            return true;
        }
        if (request.method !== "PATCH") {
            json_response(response, 405, { error: "method not allowed" });
            return true;
        }
        const body = await request_body(request) as { name?: unknown };
        const name = typeof body.name === "string" ? body.name.trim() : "";
        if (!valid_mount_name(name)) {
            json_response(response, 400, { error: "mount name must start with a letter or number and use only letters, numbers, spaces, dots, dashes, and underscores" });
            return true;
        }
        try {
            const result = await database_pool.query(
                `update infinity_storage.mounts
                 set name = $1, updated_at = now()
                 where id = $2 and user_id = $3
                 returning id, name, project_id as "projectId"`,
                [name, path_match[1], user_id],
            );
            if (result.rowCount !== 1) {
                json_response(response, 404, { error: "mount not found" });
                return true;
            }
            json_response(response, 200, { mount: result.rows[0] });
        } catch (error) {
            if ((error as { code?: string }).code === "23505") {
                json_response(response, 409, { error: "mount name already exists" });
                return true;
            }
            throw error;
        }
        return true;
    }

    if (request.method === "GET") {
        const result = await database_pool.query(
            `select id, name, project_id as "projectId"
             from infinity_storage.mounts
             where user_id = $1
             order by created_at asc`,
            [user_id],
        );
        json_response(response, 200, { mounts: result.rows });
        return true;
    }

    if (request.method !== "POST") {
        json_response(response, 405, { error: "method not allowed" });
        return true;
    }

    const body = await request_body(request) as { name?: unknown; projectId?: unknown };
    const name = typeof body.name === "string" ? body.name.trim() : "";
    const project_id = typeof body.projectId === "string" ? body.projectId : "";
    if (!valid_mount_name(name)) {
        json_response(response, 400, { error: "mount name must start with a letter or number and use only letters, numbers, spaces, dots, dashes, and underscores" });
        return true;
    }
    if (project_id.length === 0) {
        json_response(response, 400, { error: "project is required" });
        return true;
    }
    const project = await database_pool.query(
        "select id from infinity_storage.projects where id = $1 and user_id = $2",
        [project_id, user_id],
    );
    if (project.rowCount !== 1) {
        json_response(response, 404, { error: "project not found" });
        return true;
    }
    try {
        const result = await database_pool.query(
            `insert into infinity_storage.mounts (id, user_id, project_id, name)
             values ($1, $2, $3, $4)
             on conflict (user_id, project_id) do update
             set name = excluded.name, updated_at = now()
             returning id, name, project_id as "projectId"`,
            [randomUUID(), user_id, project_id, name],
        );
        json_response(response, 200, { mount: result.rows[0] });
    } catch (error) {
        if ((error as { code?: string }).code === "23505") {
            json_response(response, 409, { error: "mount name already exists" });
            return true;
        }
        throw error;
    }
    return true;
}

async function handle_projects(
    request: import("node:http").IncomingMessage,
    response: import("node:http").ServerResponse,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", config.auth_url);
    if (url.pathname !== "/v1/projects") return false;

    const user_id = await request_user_id(request);
    if (user_id === null) {
        json_response(response, 401, { error: "unauthorized" });
        return true;
    }

    if (request.method === "GET") {
        const result = await database_pool.query<{ id: string; name: string }>(
            "select id, name from infinity_storage.projects where user_id = $1 order by created_at asc",
            [user_id],
        );
        json_response(response, 200, {
            projects: result.rows.map((project) => ({
                ...project,
                prefix: `${user_id}/${project.id}`,
            })),
        });
        return true;
    }

    if (request.method !== "POST") {
        json_response(response, 405, { error: "method not allowed" });
        return true;
    }

    const body = await request_body(request);
    const name = typeof (body as { name?: unknown }).name === "string"
        ? (body as { name: string }).name.trim()
        : "";
    if (name.length === 0 || name.length > 100) {
        json_response(response, 400, { error: "project name must be 1-100 characters" });
        return true;
    }

    const project = { id: randomUUID(), name, prefix: "" };
    try {
        await database_pool.query(
            "insert into infinity_storage.projects (id, user_id, name) values ($1, $2, $3)",
            [project.id, user_id, project.name],
        );
    } catch (error) {
        if ((error as { code?: string }).code === "23505") {
            json_response(response, 409, { error: "project name already exists" });
            return true;
        }
        throw error;
    }
    project.prefix = `${user_id}/${project.id}`;
    json_response(response, 201, { project });
    return true;
}

function html_response(
    response: import("node:http").ServerResponse,
    status: number,
    body: string,
): void {
    response.writeHead(status, {
        "cache-control": "no-store",
        "content-security-policy": [
            "base-uri 'none'",
            "connect-src 'self'",
            "default-src 'none'",
            "script-src 'unsafe-inline'",
            "style-src 'none'",
        ].join("; "),
        "content-type": "text/html; charset=utf-8",
        "x-content-type-options": "nosniff",
    });
    response.end(body);
}

function desktop_callback_url(redirect: string): string {
    const callback = new URL("/desktop/bridge", config.auth_url);
    callback.searchParams.set("redirect", redirect);
    return callback.toString();
}

function desktop_callback_is_valid(redirect: string | null): redirect is string {
    if (redirect === null) return false;
    return /^http:\/\/127\.0\.0\.1:\d+\/callback$/.test(redirect);
}

function desktop_sign_in_page(callback_url: string): string {
    const body = JSON.stringify({ provider: "google", callbackURL: callback_url });
    return `<!doctype html><meta charset="utf-8"><title>Infinity Storage</title>
<body><p id="status">Opening Google sign-in…</p><script>
void (async () => {
    const response = await fetch("/api/auth/sign-in/social", {
        method: "POST",
        headers: { "content-type": "application/json" },
        credentials: "same-origin",
        body: ${JSON.stringify(body)},
    });
    if (!response.ok) {
        const detail = await response.text();
        throw new Error("sign-in request failed (" + response.status + "): " + detail.slice(0, 300));
    }
    const payload = await response.json();
    if (typeof payload.url !== "string") throw new Error("sign-in URL missing");
    location.assign(payload.url);
})().catch((error) => {
    document.getElementById("status").textContent = error instanceof Error ? error.message : "Could not start sign-in.";
});
</script>`;
}

async function handle_desktop_sign_in(
    request: import("node:http").IncomingMessage,
    response: import("node:http").ServerResponse,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", config.auth_url);
    if (url.pathname !== "/desktop/sign-in") return false;
    if (request.method !== "GET") {
        json_response(response, 405, { error: "method not allowed" });
        return true;
    }
    const redirect = url.searchParams.get("redirect");
    if (desktop_callback_is_valid(redirect) === false) {
        html_response(response, 400, "<!doctype html><title>Infinity Storage</title><p>Invalid sign-in redirect.</p>");
        return true;
    }
    html_response(response, 200, desktop_sign_in_page(desktop_callback_url(redirect)));
    return true;
}

function parse_session_cookie(header: string | undefined): string | null {
    if (header === undefined) return null;
    for (const part of header.split(";")) {
        const eq = part.indexOf("=");
        if (eq === -1) continue;
        const name = part.slice(0, eq).trim();
        if (name.endsWith(".session_token")) {
            const value = part.slice(eq + 1).trim();
            return value.length > 0 ? value : null;
        }
    }
    return null;
}

async function handle_desktop_bridge(
    request: import("node:http").IncomingMessage,
    response: import("node:http").ServerResponse,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", config.auth_url);
    if (url.pathname !== "/desktop/bridge") return false;

    const redirect = url.searchParams.get("redirect");
    if (desktop_callback_is_valid(redirect) === false) {
        html_response(response, 400, "<!doctype html><title>Infinity Storage</title><p>Invalid bridge redirect.</p>");
        return true;
    }

    const [session, token] = await Promise.all([
        auth.api.getSession({ headers: request_headers(request) }).catch(() => null),
        Promise.resolve(parse_session_cookie(request.headers.cookie)),
    ]);
    if (session === null || token === null) {
        html_response(response, 401, "<!doctype html><title>Infinity Storage</title><p>Sign-in failed. Close this tab and try again.</p>");
        return true;
    }

    const target = `${redirect}#token=${encodeURIComponent(token)}`;
    html_response(
        response,
        200,
        `<!doctype html><title>Infinity Storage</title><p>Signing you in…</p><script>location.replace(${JSON.stringify(target)})</script>`,
    );
    return true;
}

const server = createServer((request, response) => {
    void (async () => {
        if (await handle_desktop_sign_in(request, response)) return;
        if (await handle_desktop_bridge(request, response)) return;
        if (await handle_library_authorization(request, response)) return;
        if (await handle_mounts(request, response)) return;
        if (await handle_projects(request, response)) return;
        await auth_handler(request, response);
    })().catch(() => {
        if (response.headersSent === false) {
            response.writeHead(500, { "content-type": "text/plain; charset=utf-8" });
        }
        response.end("Internal server error");
    });
});

server.headersTimeout = 15_000;
server.keepAliveTimeout = 5_000;
server.requestTimeout = 15_000;
server.maxHeadersCount = 64;

async function shutdown(signal: NodeJS.Signals): Promise<void> {
    const timeout = setTimeout(() => process.exit(1), SHUTDOWN_TIMEOUT_MS);
    timeout.unref();
    server.close();
    await database_pool.end();
    clearTimeout(timeout);
    process.stdout.write(`Infinity Storage API stopped (${signal})\n`);
}

server.listen(config.port, config.host, () => {
    process.stdout.write(`Infinity Storage API listening on ${config.auth_url}\n`);
});

process.once("SIGINT", () => void shutdown("SIGINT"));
process.once("SIGTERM", () => void shutdown("SIGTERM"));
