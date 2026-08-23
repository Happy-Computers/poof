const PORT_MIN = 1;
const PORT_MAX = 65_535;
const SECRET_BYTES_MIN = 32;

export type Config = Readonly<{
    auth_url: string;
    database_url: string;
    google_client_id: string;
    google_client_secret: string;
    host: string;
    port: number;
    secret: string;
}>;

function require_env(env: NodeJS.ProcessEnv, name: string): string {
    const value = env[name];
    if (typeof value !== "string") {
        throw new Error(`${name} is required`);
    }
    if (value.length === 0) {
        throw new Error(`${name} is required`);
    }
    return value;
}

function parse_port(value: string): number {
    const port = Number(value);
    if (Number.isInteger(port) === false) {
        throw new Error("PORT must be an integer");
    }
    if (port < PORT_MIN || port > PORT_MAX) {
        throw new Error(`PORT must be between ${PORT_MIN} and ${PORT_MAX}`);
    }
    return port;
}

function parse_host(value: string): string {
    const hosts = new Set(["127.0.0.1", "0.0.0.0", "::1", "::"]);
    if (hosts.has(value) === false) {
        throw new Error("HOST must be a valid bind address");
    }
    return value;
}

function parse_url(value: string, name: string, protocols: ReadonlySet<string>): URL {
    const url = new URL(value);
    if (protocols.has(url.protocol) === false) {
        throw new Error(`${name} has an invalid protocol`);
    }
    if (url.hostname.length === 0) {
        throw new Error(`${name} must include a hostname`);
    }
    return url;
}

export function load_config(env: NodeJS.ProcessEnv): Config {
    const auth_url = require_env(env, "BETTER_AUTH_URL");
    const database_url = require_env(env, "DATABASE_URL");
    const secret = require_env(env, "BETTER_AUTH_SECRET");

    parse_url(auth_url, "BETTER_AUTH_URL", new Set(["http:", "https:"]));
    parse_url(database_url, "DATABASE_URL", new Set(["postgres:", "postgresql:"]));
    if (Buffer.byteLength(secret, "utf8") < SECRET_BYTES_MIN) {
        throw new Error(`BETTER_AUTH_SECRET must be at least ${SECRET_BYTES_MIN} bytes`);
    }

    return {
        auth_url,
        database_url,
        google_client_id: require_env(env, "GOOGLE_CLIENT_ID"),
        google_client_secret: require_env(env, "GOOGLE_CLIENT_SECRET"),
        host: parse_host(env.HOST ?? "127.0.0.1"),
        port: parse_port(env.PORT ?? "3005"),
        secret,
    };
}
