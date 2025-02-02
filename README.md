[![Go
Reference](https://pkg.go.dev/badge/lesiw.io/chrono.svg)](https://pkg.go.dev/lesiw.io/chrono)

# lesiw.io/chrono

A simple cronjob scheduler to handle job execution across multiple running
copies of an application. Currently only `github.com/jackc/pgx/v5` is supported
as a backing store.

Instead of _goroutines_, `lesiw.io/chrono` schedules _chronoroutines_. Like
goroutines, chronoroutines have a deliberately simple API and offer little
configuration about how they are executed beyond their cron expression.

If a node dies partway through job execution, the job will be restarted by
another node after a minute of missing heartbeats.

Jobs do not immediately fire upon being scheduled. However, if
`lesiw.io/chrono` detects that a cron tick was missed, it will execute the
missed job immediately.

For assistance with crontab syntax, check out https://crontab.guru.

## Minimal example

```go
package main

import (
    "context"
    "log"

    "github.com/jackc/pgx/v5"
    "lesiw.io/chrono"
)

func main() {
    conn, err := pgx.Connect(context.Background(), "")
    if err != nil {
        log.Fatal(err)
    }
    cron, err := chrono.NewPgx(conn)
    if err != nil {
        log.Fatal(err)
    }
    err = cron.Go("example", "* * * * *", func() { println("hello world!") })
    if err != nil {
        log.Fatal(err)
    }
    select {}
}
```

A more full-featured example is available under `internal/example`. Run
`docker compose up` to build it.