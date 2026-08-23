create table infinity_storage_auth.desktop_auth_challenges (
    id uuid primary key,
    verifier_hash bytea not null check (octet_length(verifier_hash) = 32),
    session_token text check (session_token is null or length(session_token) between 1 and 512),
    expires_at timestamptz not null,
    created_at timestamptz not null default now()
);

create index desktop_auth_challenges_expires_at_idx
    on infinity_storage_auth.desktop_auth_challenges (expires_at);

revoke all on infinity_storage_auth.desktop_auth_challenges from anon, authenticated;
