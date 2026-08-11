set search_path to infinity_storage_auth;

create table "user" (
    "id" text primary key,
    "name" text not null,
    "email" text not null unique,
    "emailVerified" boolean not null,
    "image" text,
    "createdAt" timestamptz not null,
    "updatedAt" timestamptz not null
);

create table "session" (
    "id" text primary key,
    "expiresAt" timestamptz not null,
    "token" text not null unique,
    "createdAt" timestamptz not null,
    "updatedAt" timestamptz not null,
    "ipAddress" text,
    "userAgent" text,
    "userId" text not null references "user" ("id") on delete cascade
);

create index "session_userId_idx" on "session" ("userId");

create table "account" (
    "id" text primary key,
    "accountId" text not null,
    "providerId" text not null,
    "userId" text not null references "user" ("id") on delete cascade,
    "accessToken" text,
    "refreshToken" text,
    "idToken" text,
    "accessTokenExpiresAt" timestamptz,
    "refreshTokenExpiresAt" timestamptz,
    "scope" text,
    "password" text,
    "createdAt" timestamptz not null,
    "updatedAt" timestamptz not null
);

create index "account_userId_idx" on "account" ("userId");
create unique index "account_provider_account_idx"
    on "account" ("providerId", "accountId");

create table "verification" (
    "id" text primary key,
    "identifier" text not null,
    "value" text not null,
    "expiresAt" timestamptz not null,
    "createdAt" timestamptz not null,
    "updatedAt" timestamptz not null
);

create index "verification_identifier_idx" on "verification" ("identifier");

create table "rateLimit" (
    "id" text primary key,
    "key" text not null unique,
    "count" integer not null,
    "lastRequest" bigint not null
);

revoke all on all tables in schema infinity_storage_auth from anon, authenticated;
