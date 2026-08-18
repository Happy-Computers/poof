const EMAIL_BYTES_MAX = 320;
const SUBJECT_BYTES_MAX = 120;
const TEXT_BYTES_MAX = 8_192;

export type EmailConfig = Readonly<{
    api_key: string;
    from: string;
}>;

export type Email = Readonly<{
    subject: string;
    text: string;
    to: string;
}>;

function assert_bound(value: string, bytes_max: number, name: string): void {
    if (Buffer.byteLength(value, "utf8") > bytes_max) {
        throw new Error(`${name} exceeds ${bytes_max} bytes`);
    }
}

export async function send_email(config: EmailConfig, email: Email): Promise<void> {
    assert_bound(config.from, EMAIL_BYTES_MAX, "from");
    assert_bound(email.to, EMAIL_BYTES_MAX, "to");
    assert_bound(email.subject, SUBJECT_BYTES_MAX, "subject");
    assert_bound(email.text, TEXT_BYTES_MAX, "text");

    const response = await fetch("https://api.resend.com/emails", {
        body: JSON.stringify({
            from: config.from,
            subject: email.subject,
            text: email.text,
            to: [email.to],
        }),
        headers: {
            authorization: `Bearer ${config.api_key}`,
            "content-type": "application/json",
        },
        method: "POST",
        signal: AbortSignal.timeout(10_000),
    });
    if (response.ok === false) {
        throw new Error(`email delivery failed with status ${response.status}`);
    }
}
