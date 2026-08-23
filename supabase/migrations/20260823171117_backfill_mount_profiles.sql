insert into infinity_storage.mounts (id, user_id, project_id, name)
select p.id,
       p.user_id,
       p.id,
       case
           when p.name ~ '^[A-Za-z0-9][A-Za-z0-9 _.-]{0,99}$' then p.name
           else 'mount-' || substring(p.id from 1 for 8)
       end
from infinity_storage.projects p
where not exists (
    select 1
    from infinity_storage.mounts m
    where m.project_id = p.id
)
on conflict (user_id, project_id) do nothing;
