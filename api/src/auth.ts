import { betterAuth } from "better-auth";
import { bearer } from "better-auth/plugins";
import { Pool } from "pg";
import { load_config } from "./config.js";

const config = load_config(process.env);

export const database_pool = new Pool({
    allowExitOnIdle: false,
    connectionString: config.database_url,
    connectionTimeoutMillis: 10_000,
    idleTimeoutMillis: 30_000,
    max: 10,
    options: "-c search_path=infinity_storage_auth",
});

const DESKTOP_ORIGIN = process.env.DESKTOP_ORIGIN ?? "http://127.0.0.1:9778";

export const auth = betterAuth({
    appName: "Infinity Storage",
    baseURL: config.auth_url,
    database: database_pool,
    plugins: [bearer()],
    rateLimit: {
        enabled: true,
        max: 20,
        storage: "database",
        window: 60,
    },
    secret: config.secret,
    socialProviders: {
        google: {
            clientId: config.google_client_id,
            clientSecret: config.google_client_secret,
        },
    },
    trustedOrigins: [DESKTOP_ORIGIN],
});
