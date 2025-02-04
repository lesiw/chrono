package chrono

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/adhocore/gronx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lesiw.io/chrono/internal/stmt"
)

var sleep = time.Sleep
var timeNow = time.Now
var prevTick = gronx.PrevTick
var prevTickBefore = gronx.PrevTickBefore
var errBadCron = fmt.Errorf("bad cron expression")

// A PgxConn is a pgx.Conn or pgxpool.Pool.
type PgxConn interface {
	Exec(ctx context.Context, sql string, arguments ...any) (
		pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Pgx is a postgres-backed scheduler.
type Pgx struct {
	Log      io.Writer
	ctx      context.Context
	conn     PgxConn
	routinec chan routine
	routines map[string]routine
	notify   chan struct{}
}

// NewPgx instantiates a new Pgx scheduler.
func NewPgx(conn PgxConn) (Scheduler, error) {
	p := new(Pgx)
	return p, p.Init(conn)
}

func (p *Pgx) Init(conn PgxConn) (err error) {
	p.ctx = context.Background()
	p.conn = conn
	p.routinec = make(chan routine)
	p.routines = make(map[string]routine)
	for n := range 3 {
		_, err = conn.Exec(p.ctx, stmt.CreateChronoTable)
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
	return nil
}

func (p *Pgx) Go(name, cron string, f func()) error {
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
	w := cmp.Or[io.Writer](p.Log, os.Stderr)
	_, _ = w.Write([]byte(fmt.Sprintf("lesiw.io/chrono: %s\n", a)))
}

func (p *Pgx) insert(r routine) error {
	lastrun, err := prevTick(r.Expr, false)
	if err != nil {
		return fmt.Errorf("could not determine previous tick: %w", err)
	}
	_, err = p.conn.Exec(p.ctx, stmt.InsertJob, r.Name, lastrun)
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
	tx, err := p.conn.Begin(p.ctx)
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
				_, err = p.conn.Exec(
					p.ctx, stmt.HeartbeatJob, r.Name, timeNow(),
				)
				if err != nil {
					p.log(err)
				}
			}
		}
	done:
		_, err = p.conn.Exec(
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
