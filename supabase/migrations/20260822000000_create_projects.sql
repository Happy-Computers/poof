create schema if not exists infinity_storage;
revoke all on schema infinity_storage from public;

create table if not exists infinity_storage.projects (
    id text primary key,
    user_id text not null,
    name text not null,
    created_at timestamptz not null default now(),
    unique (user_id, name)
);

revoke all on all tables in schema infinity_storage from anon, authenticated;
