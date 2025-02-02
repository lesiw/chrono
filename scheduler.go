package chrono

//go:generate go run lesiw.io/plain/cmd/plaingen@latest
//go:generate rm migration.go

import (
	"fmt"
	"io"
	"time"

	"github.com/adhocore/gronx"
)

type Scheduler interface {
	Go(name, expr string, f func()) error
}

type routine struct {
	Name  string
	Expr  string
	Do    func()
	Error chan error
}

// Memory is an in-memory scheduler.
// Its primary purpose is testing and documentation.
// For durable execution, use a persistent store.
type Memory struct {
	Log      io.Writer
	routines chan routine
}

func New() Scheduler {
	m := &Memory{routines: make(chan routine)}
	m.Init()
	return m
}

func (m *Memory) Init() {
	go func() {
		var routines []routine
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		gron := gronx.New()
		for {
			select {
			case r := <-m.routines:
				routines = append(routines, r)
				r.Error <- nil
			case <-ticker.C:
				now := time.Now()
				for _, r := range routines {
					if ok, err := gron.IsDue(r.Expr, now); err != nil && ok {
						go r.Do()
					}
				}
			}
		}
	}()
}

func (m *Memory) Go(name, cron string, f func()) error {
	if !gronx.IsValid(cron) {
		return fmt.Errorf("bad cron expression")
	}
	r := routine{
		Name:  name,
		Expr:  cron,
		Do:    f,
		Error: make(chan error),
	}
	m.routines <- r
	return <-r.Error
}
