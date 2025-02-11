// Package chrono schedules durable cron jobs backed by data stores.
package chrono

import (
	"fmt"
	"time"

	"github.com/adhocore/gronx"
)

//go:generate go run lesiw.io/plain/cmd/plaingen@latest
//go:generate rm migration.go

var (
	sleep          = time.Sleep
	timeNow        = time.Now
	prevTick       = gronx.PrevTick
	prevTickBefore = gronx.PrevTickBefore
	errBadCron     = fmt.Errorf("bad cron expression")
)

type routine struct {
	Name  string
	Expr  string
	Do    func()
	Error chan error
}
