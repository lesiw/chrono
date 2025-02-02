insert into _chrono (
    name,
    active,
    last_run,
    last_heartbeat
)
values (
    $1,
    false,
    $2,
    '1970-01-01T00:00:00+00:00'::timestamptz
)
on conflict (name) do nothing
