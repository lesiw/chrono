update _chrono
set
    active = $2,
    last_run = $3,
    last_heartbeat = $4
where name = $1
