create table if not exists _chrono (
    name text primary key,
    active boolean not null,
    last_run timestamp not null,
    last_heartbeat timestamp not null
)
