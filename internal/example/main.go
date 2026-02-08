package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lesiw.io/chrono"
)

func main() {
	if err := run(); err != nil {
		println(err)
		os.Exit(1)
	}
}

func run() (err error) {
	var pool *pgxpool.Pool
	for n := range 3 {
		pool, err = pgxpool.New(context.Background(), "")
		if err != nil {
			time.Sleep(time.Duration(n) * time.Second)
			continue
		}
		break
	}
	if err != nil {
		return fmt.Errorf("could not connect to database: %w", err)
	}
	cron := chrono.Pgx{Conn: pool}
	if err := cron.Start(); err != nil {
		return err
	}
	err = cron.Go("example", "* * * * *", func() {
		slog.Info("hello world!")
	})
	if err != nil {
		return err
	}
	select {}
}
