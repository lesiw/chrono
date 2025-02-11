package chrono

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/adhocore/gronx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lesiw.io/chrono/internal/stmt"
)

// A PgxConn is a [pgx.Conn] or [pgxpool.Pool].
//
// [pgx.Conn]: https://pkg.go.dev/github.com/jackc/pgx/v5#Conn
// [pgxpool.Pool]: https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool
type PgxConn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (
		pgconn.CommandTag, error)
}

// Pgx is a PostgreSQL-backed scheduler.
//
// The Conn field is required and should be a [pgx.Conn] or [pgxpool.Pool].
//
//	cron := chrono.Pgx{Conn: conn}
//	if err := cron.Start(); err != nil {
//		log.Fatal(err)
//	}
//
// [pgx.Conn]: https://pkg.go.dev/github.com/jackc/pgx/v5#Conn
// [pgxpool.Pool]: https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool
type Pgx struct {
	Conn PgxConn
	Log  io.Writer

	started  bool
	ctx      context.Context
	routinec chan routine
	routines map[string]routine
	notify   chan struct{}
}

// Start initializes a [Pgx] scheduler.
func (p *Pgx) Start() (err error) {
	if p.started {
		return fmt.Errorf("already started")
	}
	if p.Conn == nil {
		return fmt.Errorf("bad Conn")
	}
	if p.Log == nil {
		p.Log = io.Discard
	}
	p.ctx = context.Background()
	p.routinec = make(chan routine)
	p.routines = make(map[string]routine)
	for n := range 3 {
		_, err = p.Conn.Exec(p.ctx, stmt.CreateChronoTable)
		if err != nil {
			sleep(time.Duration(math.Pow(2, float64(n))) * time.Second)
			continue
		}
		break
	}
	if err != nil {
		return fmt.Errorf("could not create chrono table: %w", err)
	}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		for {
			select {
			case r := <-p.routinec:
				p.routines[r.Name] = r
				r.Error <- p.insert(r)
			case <-ticker.C:
				now := time.Now()
				for _, r := range p.routines {
					if err := p.tick(now, r); err != nil {
						p.log(err)
					}
				}
			}
		}
	}()
	p.started = true
	return nil
}

// Go schedules a task.
//
// name is the unique identifier for this task.
//
// The task will run on the next tick of the cron expression.
// If [Pgx] detects a missed cron tick, it runs the task immediately.
func (p *Pgx) Go(name, cron string, f func()) error {
	if !p.started {
		return fmt.Errorf("not started")
	}
	if !gronx.IsValid(cron) {
		return errBadCron
	}
	r := routine{
		Name:  name,
		Expr:  cron,
		Do:    f,
		Error: make(chan error),
	}
	p.routinec <- r
	return <-r.Error
}

func (p *Pgx) log(a any) {
	_, _ = p.Log.Write([]byte(fmt.Sprintf("lesiw.io/chrono: %s\n", a)))
}

func (p *Pgx) insert(r routine) error {
	lastrun, err := prevTick(r.Expr, false)
	if err != nil {
		return fmt.Errorf("could not determine previous tick: %w", err)
	}
	_, err = p.Conn.Exec(p.ctx, stmt.InsertJob, r.Name, lastrun)
	if err != nil {
		return fmt.Errorf("InsertJob: %w", err)
	}
	return nil
}

func (p *Pgx) tick(now time.Time, r routine) error {
	tick, err := prevTickBefore(r.Expr, now, true)
	if err != nil {
		return err
	}
	var active bool
	var lastRun, lastBeat time.Time
	tx, err := p.Conn.Begin(p.ctx)
	if err != nil {
		return err
	}
	defer tx.Commit(p.ctx)
	err = tx.QueryRow(
		p.ctx, stmt.SelectJob, r.Name,
	).Scan(&active, &lastRun, &lastBeat)
	if err != nil {
		return fmt.Errorf("SelectJob: %w", err)
	}
	if !tick.After(lastRun) {
		return nil
	}
	if active && lastBeat.After(now.Add(-time.Minute)) {
		return nil
	}
	_, err = tx.Exec(p.ctx, stmt.ActivateJob, r.Name, now)
	if err != nil {
		return fmt.Errorf("ActivateJob: %w", err)
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				goto done
			case <-ticker.C:
				_, err = p.Conn.Exec(
					p.ctx, stmt.HeartbeatJob, r.Name, timeNow(),
				)
				if err != nil {
					p.log(err)
				}
			}
		}
	done:
		_, err = p.Conn.Exec(
			p.ctx, stmt.UpdateJob, r.Name, false, now, timeNow(),
		)
		if err != nil {
			p.log(err)
		}
		select {
		case p.notify <- struct{}{}:
		default:
		}
	}()
	go func() { r.Do(); done <- struct{}{} }()
	return nil
}
