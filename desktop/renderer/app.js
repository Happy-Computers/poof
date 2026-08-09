"use strict";

const PATH_BYTES_MAX = 4096;
const BUCKET_BYTES_MAX = 256;

function assert(condition, message) {
    if (condition) {
        return;
    }
    throw new Error(message);
}

function element(id, constructor) {
    const value = document.getElementById(id);
    assert(value instanceof constructor, `${id} missing`);
    return value;
}

function assert_bound(value, bytes_max, label) {
    assert(typeof value === "string", `${label} must be a string`);
    assert(new TextEncoder().encode(value).length <= bytes_max, `${label} too long`);
}

function default_mount_path() {
    return navigator.userAgent.includes("Windows") ? "Z:" : "/tmp/infinity-storage";
}

const el_auth_name = element("auth_name", HTMLInputElement);
const el_auth_email = element("auth_email", HTMLInputElement);
const el_auth_password = element("auth_password", HTMLInputElement);
const el_auth_signed_out = element("auth_signed_out", HTMLElement);
const el_auth_signed_in = element("auth_signed_in", HTMLElement);
const el_auth_identity = element("auth_identity", HTMLElement);
const el_auth_status = element("auth_status", HTMLElement);
const el_mount_controls = element("mount_controls", HTMLElement);
const el_mount_path = element("mount_path", HTMLInputElement);
const el_bucket = element("bucket", HTMLInputElement);
const el_status = element("status", HTMLElement);
const el_btn_sign_in = element("btn_sign_in", HTMLButtonElement);
const el_btn_sign_up = element("btn_sign_up", HTMLButtonElement);
const el_btn_sign_out = element("btn_sign_out", HTMLButtonElement);
const el_btn_reset = element("btn_reset", HTMLButtonElement);
const el_btn_mount = element("btn_mount", HTMLButtonElement);
const el_btn_unmount = element("btn_unmount", HTMLButtonElement);
const el_btn_open = element("btn_open", HTMLButtonElement);

assert(typeof window.infinity_storage === "object", "preload bridge missing");
el_mount_path.value = default_mount_path();

function paint_auth(session_data) {
    const signed_in = typeof session_data?.user?.email === "string";
    el_auth_signed_out.hidden = signed_in;
    el_auth_signed_in.hidden = !signed_in;
    el_mount_controls.hidden = !signed_in;
    el_auth_identity.textContent = signed_in ? session_data.user.email : "";
}

function paint_auth_status(state, text) {
    el_auth_status.dataset.state = state;
    el_auth_status.textContent = text;
}

async function refresh_auth() {
    const session_data = await window.infinity_storage.auth_session();
    paint_auth(session_data);
}

function read_credentials(include_name) {
    const email = el_auth_email.value.trim();
    const password = el_auth_password.value;
    assert(el_auth_email.checkValidity(), "valid email required");
    assert(password.length >= 12, "password must be at least 12 characters");
    assert(password.length <= 128, "password must be at most 128 characters");
    if (include_name) {
        const name = el_auth_name.value.trim();
        assert(name.length > 0, "name required");
        return { email, name, password };
    }
    return { email, password };
}

async function run_auth(action, success_text) {
    paint_auth_status("idle", "Working…");
    try {
        await action();
        paint_auth_status("mounted", success_text);
        await refresh_auth();
    } catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        paint_auth_status("error", message);
    }
}

function paint_mount_status(payload) {
    assert(typeof payload?.state === "string", "status.state required");
    assert(typeof payload?.text === "string", "status.text required");
    el_status.dataset.state = payload.state;
    el_status.textContent = payload.text;
    const mounted = payload.state === "mounted";
    el_btn_mount.disabled = mounted;
    el_btn_unmount.disabled = !mounted;
}

function read_mount_form() {
    const mount_path = el_mount_path.value.trim();
    const bucket = el_bucket.value.trim();
    assert_bound(mount_path, PATH_BYTES_MAX, "mount_path");
    assert_bound(bucket, BUCKET_BYTES_MAX, "bucket");
    assert(mount_path.length > 0, "mount path required");
    return { mount_path, bucket };
}

el_btn_sign_in.addEventListener("click", () => {
    void run_auth(
        () => window.infinity_storage.auth_sign_in(read_credentials(false)),
        "Signed in",
    );
});

el_btn_sign_up.addEventListener("click", () => {
    void run_auth(
        () => window.infinity_storage.auth_sign_up(read_credentials(true)),
        "Check your email to verify the account",
    );
});

el_btn_sign_out.addEventListener("click", () => {
    void run_auth(() => window.infinity_storage.auth_sign_out(), "Signed out");
});

el_btn_reset.addEventListener("click", () => {
    void run_auth(() => {
        assert(el_auth_email.checkValidity(), "valid email required");
        return window.infinity_storage.auth_reset(el_auth_email.value.trim());
    }, "Check your email for the reset link");
});

el_btn_mount.addEventListener("click", () => {
    void (async () => {
        try {
            const options = read_mount_form();
            assert(options.bucket.length > 0, "bucket required");
            paint_mount_status(await window.infinity_storage.mount(options));
        } catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            paint_mount_status({ state: "error", text: message });
        }
    })();
});

el_btn_unmount.addEventListener("click", () => {
    void window.infinity_storage.unmount().then(paint_mount_status);
});

el_btn_open.addEventListener("click", () => {
    void (async () => {
        try {
            const options = read_mount_form();
            paint_mount_status(await window.infinity_storage.open_folder(options.mount_path));
        } catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            paint_mount_status({ state: "error", text: message });
        }
    })();
});

window.infinity_storage.on_status(paint_mount_status);
void window.infinity_storage.get_status().then(paint_mount_status);
void refresh_auth().catch((error) => {
    paint_auth(null);
    paint_auth_status("error", error instanceof Error ? error.message : String(error));
});
