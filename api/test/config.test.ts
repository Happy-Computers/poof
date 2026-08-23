import assert from "node:assert/strict";
import test from "node:test";
import { load_config } from "../src/config.js";

const valid_env = {
    BETTER_AUTH_SECRET: "01234567890123456789012345678901",
    BETTER_AUTH_URL: "http://127.0.0.1:3005",
    DATABASE_URL: "postgresql://postgres:password@localhost:5432/postgres",
    GOOGLE_CLIENT_ID: "test-client-id",
    GOOGLE_CLIENT_SECRET: "test-client-secret",
    PORT: "3005",
};

test("load_config accepts bounded valid configuration", () => {
    const config = load_config(valid_env);
    assert.equal(config.port, 3005);
    assert.equal(config.auth_url, valid_env.BETTER_AUTH_URL);
});

test("load_config rejects a short secret", () => {
    assert.throws(
        () => load_config({ ...valid_env, BETTER_AUTH_SECRET: "short" }),
        /at least 32 bytes/,
    );
});

test("load_config rejects a non-Postgres database URL", () => {
    assert.throws(
        () => load_config({ ...valid_env, DATABASE_URL: "https://example.com" }),
        /invalid protocol/,
    );
});

test("load_config rejects an out-of-range port", () => {
    assert.throws(() => load_config({ ...valid_env, PORT: "65536" }), /between 1 and 65535/);
});
