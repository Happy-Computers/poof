import type { IncomingMessage, ServerResponse } from "node:http";
import type { auth } from "./auth.js";

const BODY_BYTES_MAX = 2_048;
const TOKEN_BYTES_MAX = 1_024;

function send_page(response: ServerResponse, status: number, body: string): void {
    const html = [
        "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\">",
        "<meta name=\"viewport\" content=\"width=device-width\">",
        "<title>Infinity Storage</title><body><main><h1>Infinity Storage</h1>",
        body,
        "</main></body></html>",
    ].join("");
    response.writeHead(status, {
        "cache-control": "no-store",
        "content-security-policy": [
            "default-src 'none'",
            "style-src 'none'",
            "form-action 'self'",
            "base-uri 'none'",
        ].join("; "),
        "content-type": "text/html; charset=utf-8",
        "x-content-type-options": "nosniff",
    });
    response.end(html);
}

function reset_form(token: string): string {
    if (/^[A-Za-z0-9._~-]+$/.test(token) === false) {
        return "<p>The reset link is invalid.</p>";
    }
    if (Buffer.byteLength(token, "utf8") > TOKEN_BYTES_MAX) {
        return "<p>The reset link is invalid.</p>";
    }
    return [
        "<form method=\"post\"><input type=\"hidden\" name=\"token\" value=\"",
        token,
        "\"><label>New password <input name=\"password\" type=\"password\" ",
        "minlength=\"12\" maxlength=\"128\" required></label>",
        "<button type=\"submit\">Reset password</button></form>",
    ].join("");
}

async function read_body(request: IncomingMessage): Promise<string> {
    const chunks: Buffer[] = [];
    let bytes = 0;
    for await (const chunk of request) {
        const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
        bytes += buffer.byteLength;
        if (bytes > BODY_BYTES_MAX) {
            throw new Error("request body too large");
        }
        chunks.push(buffer);
    }
    return Buffer.concat(chunks, bytes).toString("utf8");
}

export async function handle_reset_password(
    request: IncomingMessage,
    response: ServerResponse,
    auth_instance: typeof auth,
): Promise<boolean> {
    const url = new URL(request.url ?? "/", "http://127.0.0.1");
    if (url.pathname !== "/reset-password") {
        return false;
    }
    if (request.method === "GET") {
        send_page(response, 200, reset_form(url.searchParams.get("token") ?? ""));
        return true;
    }
    if (request.method !== "POST") {
        send_page(response, 405, "<p>Method not allowed.</p>");
        return true;
    }
    const body = new URLSearchParams(await read_body(request));
    const password = body.get("password") ?? "";
    const token = body.get("token") ?? "";
    if (password.length < 12 || password.length > 128) {
        send_page(response, 400, "<p>Password must contain 12–128 characters.</p>");
        return true;
    }
    await auth_instance.api.resetPassword({ body: { newPassword: password, token } });
    send_page(response, 200, "<p>Password reset. Return to the desktop app to sign in.</p>");
    return true;
}
