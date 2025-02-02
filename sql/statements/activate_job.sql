update _chrono
set
    active = true,
    last_heartbeat = $2
where name = $1
