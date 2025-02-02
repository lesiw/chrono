update _chrono
set
    last_heartbeat = $2
where name = $1
