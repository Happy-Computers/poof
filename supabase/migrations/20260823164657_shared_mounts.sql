create table if not exists infinity_storage.mounts (
    id text primary key,
    user_id text not null,
    project_id text not null references infinity_storage.projects (id) on delete cascade,
    name text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (user_id, project_id),
    unique (user_id, name),
    check (name ~ '^[A-Za-z0-9][A-Za-z0-9 _.-]{0,99}$')
);

create index if not exists mounts_user_id_idx
    on infinity_storage.mounts (user_id);

create index if not exists mounts_project_id_idx
    on infinity_storage.mounts (project_id);

insert into infinity_storage.mounts (id, user_id, project_id, name)
select id,
       user_id,
       id,
       case
           when name ~ '^[A-Za-z0-9][A-Za-z0-9 _.-]{0,99}$' then name
           else 'mount-' || substring(id from 1 for 8)
       end
from infinity_storage.projects
on conflict (user_id, project_id) do nothing;

revoke all on infinity_storage.mounts from anon, authenticated;
