select
    active, last_run, last_heartbeat
from _chrono
where name = $1
for update
