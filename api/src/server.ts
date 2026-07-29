import { createServer } from "node:http";
import { toNodeHandler } from "better-auth/node";
import { auth, database_pool } from "./auth.js";
import { load_config } from "./config.js";
import { handle_reset_password } from "./reset_password.js";

const SHUTDOWN_TIMEOUT_MS = 5_000;
const config = load_config(process.env);
const auth_handler = toNodeHandler(auth);
const server = createServer((request, response) => {
    const route = async (): Promise<void> => {
        const handled = await handle_reset_password(request, response, auth);
        if (handled === false) {
            await auth_handler(request, response);
        }
    };
    void route().catch(() => {
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
