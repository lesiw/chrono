package chrono

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"lesiw.io/chrono/internal/stmt"
)

type unary struct {
	Ctx context.Context
}

type query struct {
	Ctx context.Context
	Sql string //revive:disable-line:var-naming
	Arg []any
}

type fakeConn struct {
	pgx.Tx

	begins  []unary
	commits []unary

	queries []query

	queryErrs []error
	scans     [][]any

	mu sync.Mutex
}

func (c *fakeConn) Begin(ctx context.Context) (pgx.Tx, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.begins = append(c.begins, unary{ctx})
	return c, nil
}

func (c *fakeConn) Commit(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commits = append(c.commits, unary{ctx})
	return nil
}

func (c *fakeConn) Exec(
	ctx context.Context, sql string, a ...any,
) (tag pgconn.CommandTag, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queryErrs) > len(c.queries) {
		err = c.queryErrs[len(c.queries)]
	}
	c.queries = append(c.queries, query{ctx, sql, a})
	return
}

func (c *fakeConn) QueryRow(
	ctx context.Context, sql string, a ...any,
) (row pgx.Row) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.scans) > len(c.queries) {
		row = &fakeRow{c.scans[len(c.queries)]}
	}
	c.queries = append(c.queries, query{ctx, sql, a})
	return
}

type fakeRow struct {
	contents []any
}

func (r *fakeRow) Scan(dest ...any) error {
	for i, d := range dest {
		reflect.ValueOf(d).Elem().Set(reflect.ValueOf(r.contents[i]))
	}
	return nil
}

var opts = []cmp.Option{
	cmpopts.IgnoreInterfaces(struct{ context.Context }{}),
	cmpopts.IgnoreTypes(make(chan error)),
}

func TestPgxStart(t *testing.T) {
	conn := new(fakeConn)

	cron := Pgx{Conn: conn}
	err := cron.Start()

	if err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	wantExecs := []query{{context.Background(), stmt.CreateChronoTable, nil}}
	if got, want := conn.queries, wantExecs; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
}

func TestPgxStartConnectionSlow(t *testing.T) {
	conn := new(fakeConn)
	pgxErr := errors.New("pgx error")
	conn.queryErrs = []error{pgxErr, pgxErr, nil}
	sleeps := []time.Duration{}
	swap(t, &sleep, func(d time.Duration) { sleeps = append(sleeps, d) })

	cron := Pgx{Conn: conn}
	err := cron.Start()

	if err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	var wantExecs []query
	for range 3 {
		wantExecs = append(wantExecs, query{
			context.Background(), stmt.CreateChronoTable, nil,
		})
	}
	if got, want := conn.queries, wantExecs; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	wantSleeps := []time.Duration{time.Second, 2 * time.Second}
	if got, want := sleeps, wantSleeps; !cmp.Equal(got, want, opts...) {
		t.Errorf("sleeps -want +got\n%s", cmp.Diff(want, got, opts...))
	}
}

func TestNewPgxConnectionFail(t *testing.T) {
	conn := new(fakeConn)
	pgxErr := errors.New("pgx error")
	conn.queryErrs = []error{pgxErr, pgxErr, pgxErr}
	sleeps := []time.Duration{}
	swap(t, &sleep, func(d time.Duration) { sleeps = append(sleeps, d) })

	cron := Pgx{Conn: conn}
	err := cron.Start()

	if !errors.Is(err, pgxErr) {
		t.Errorf("cron.Start() = %q, want %q", err, pgxErr)
	}
	var wantExecs []query
	for range 3 {
		wantExecs = append(wantExecs, query{
			context.Background(), stmt.CreateChronoTable, nil,
		})
	}
	if got, want := conn.queries, wantExecs; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	wantSleeps := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
	}
	if got, want := sleeps, wantSleeps; !cmp.Equal(got, want, opts...) {
		t.Errorf("sleeps -want +got\n%s", cmp.Diff(want, got, opts...))
	}
}

func TestAddRoutine(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	now := time.Now()
	swap(t, &prevTick, func(string, bool) (time.Time, error) {
		return now, nil
	})

	err := cron.Go("example", "* * * * *", nil)

	if err != nil {
		t.Errorf("cron.Go(%q, %q, func() {}) = %q, want <nil>",
			"example", "* * * * *", err)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}, {
		context.Background(),
		stmt.InsertJob,
		[]any{"example", now},
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	wantRoutines := map[string]routine{
		"example": {"example", "* * * * *", nil, make(chan error)},
	}
	gotRoutines := cron.routines
	if got, want := gotRoutines, wantRoutines; !cmp.Equal(got, want, opts...) {
		t.Errorf("routines -want +got\n%s", cmp.Diff(want, got, opts...))
	}
}

func TestAddRoutineInvalidCron(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}

	err := cron.Go("example", "bad cron", nil)

	if !errors.Is(err, errBadCron) {
		t.Errorf("cron.Go(%q, %q, func() {}) = %q, want %q",
			"example", "invalid", err, errBadCron)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	if routines := cron.routines; len(routines) > 0 {
		t.Errorf("len(routines) = %d, want 0", len(routines))
	}
}

func TestInactiveJobDue(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	cron.notify = make(chan struct{})
	now := time.Now()
	swap(t, &prevTick, func(string, bool) (time.Time, error) {
		return now.Add(time.Minute), nil
	})
	swap(t, &prevTickBefore, func(string, time.Time, bool) (time.Time, error) {
		return now, nil
	})
	swap(t, &timeNow, func() time.Time { return now })
	var execs int
	if err := cron.Go("example", "* * * * *", func() { execs++ }); err != nil {
		t.Fatalf("cron.Go(%q, %q, func() {}) = %q, want <nil>",
			"example", "* * * * *", err)
	}
	cr := cron.routines["example"]
	conn.scans = [][]any{
		{},
		{},
		{
			false, // active
			now,   // lastRun
			now,   // lastBeat
		},
	}
	now = now.Add(time.Minute)

	err := cron.tick(now, cr)
	<-cron.notify

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if err != nil {
		t.Errorf("cron.tick(%v, %v) = %q, want <nil>", now, cr, err)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}, {
		context.Background(),
		stmt.InsertJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.SelectJob,
		[]any{"example"},
	}, {
		context.Background(),
		stmt.ActivateJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.UpdateJob,
		[]any{"example", false, now, now},
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	if got, want := execs, 1; got != want {
		t.Errorf("execs = %d, want %d", got, want)
	}
	if got, want := len(conn.commits), 1; got != want {
		t.Errorf("len(conn.commits) = %d, want %d", got, want)
	}
}

func TestInactiveJobNotDue(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	now := time.Now()
	swap(t, &prevTick, func(string, bool) (time.Time, error) {
		return now, nil
	})
	swap(t, &prevTickBefore, func(string, time.Time, bool) (time.Time, error) {
		return now, nil
	})
	var execs int
	if err := cron.Go("example", "* * * * *", func() { execs++ }); err != nil {
		t.Fatalf("cron.Go(%q, %q, func() {}) = %q, want <nil>",
			"example", "* * * * *", err)
	}
	cr := cron.routines["example"]
	conn.scans = [][]any{
		{},
		{},
		{
			false, // active
			now,   // lastRun
			now,   // lastBeat
		},
	}

	err := cron.tick(now, cr)

	if err != nil {
		t.Errorf("cron.tick(%v, %v) = %q, want <nil>", now, cr, err)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}, {
		context.Background(),
		stmt.InsertJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.SelectJob,
		[]any{"example"},
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	if got, want := execs, 0; got != want {
		t.Errorf("execs = %d, want %d", got, want)
	}
	if got, want := len(conn.commits), 1; got != want {
		t.Errorf("len(conn.commits) = %d, want %d", got, want)
	}
}

func TestActiveJobValidHeartbeat(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	now := time.Now()
	swap(t, &prevTick, func(string, bool) (time.Time, error) {
		return now, nil
	})
	swap(t, &prevTickBefore, func(string, time.Time, bool) (time.Time, error) {
		return now, nil
	})
	var execs int
	if err := cron.Go("example", "* * * * *", func() { execs++ }); err != nil {
		t.Fatalf("cron.Go(%q, %q, func() {}) = %q, want <nil>",
			"example", "* * * * *", err)
	}
	cr := cron.routines["example"]
	conn.scans = [][]any{
		{},
		{},
		{
			true,                      // active
			now.Add(-5 * time.Minute), // lastRun
			now.Add(-2 * time.Second), // lastBeat
		},
	}

	err := cron.tick(now, cr)

	if err != nil {
		t.Errorf("cron.tick(%v, %v) = %q, want <nil>", now, cr, err)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}, {
		context.Background(),
		stmt.InsertJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.SelectJob,
		[]any{"example"},
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	if got, want := execs, 0; got != want {
		t.Errorf("execs = %d, want %d", got, want)
	}
	if got, want := len(conn.commits), 1; got != want {
		t.Errorf("len(conn.commits) = %d, want %d", got, want)
	}
}

func TestActiveJobInvalidHeartbeat(t *testing.T) {
	conn := new(fakeConn)
	cron := Pgx{Conn: conn}
	if err := cron.Start(); err != nil {
		t.Errorf("cron.Start() = %q, want <nil>", err)
	}
	cron.notify = make(chan struct{})
	now := time.Now()
	swap(t, &prevTick, func(string, bool) (time.Time, error) {
		return now, nil
	})
	swap(t, &prevTickBefore, func(string, time.Time, bool) (time.Time, error) {
		return now.Add(time.Minute), nil
	})
	swap(t, &timeNow, func() time.Time { return now })
	var execs int
	if err := cron.Go("example", "* * * * *", func() { execs++ }); err != nil {
		t.Fatalf("cron.Go(%q, %q, func() {}) = %q, want <nil>",
			"example", "* * * * *", err)
	}
	cr := cron.routines["example"]
	conn.scans = [][]any{
		{},
		{},
		{
			true,                      // active
			now.Add(-5 * time.Minute), // lastRun
			now.Add(-2 * time.Minute), // lastBeat
		},
	}

	err := cron.tick(now, cr)
	<-cron.notify

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if err != nil {
		t.Errorf("cron.tick(%v, %v) = %q, want <nil>", now, cr, err)
	}
	wantQueries := []query{{
		context.Background(),
		stmt.CreateChronoTable,
		nil,
	}, {
		context.Background(),
		stmt.InsertJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.SelectJob,
		[]any{"example"},
	}, {
		context.Background(),
		stmt.ActivateJob,
		[]any{"example", now},
	}, {
		context.Background(),
		stmt.UpdateJob,
		[]any{"example", false, now, now},
	}}
	if got, want := conn.queries, wantQueries; !cmp.Equal(got, want, opts...) {
		t.Errorf("queries -want +got\n%s", cmp.Diff(want, got, opts...))
	}
	if got, want := execs, 1; got != want {
		t.Errorf("execs = %d, want %d", got, want)
	}
	if got, want := len(conn.commits), 1; got != want {
		t.Errorf("len(conn.commits) = %d, want %d", got, want)
	}
}

func swap[T any](t *testing.T, orig *T, with T) {
	t.Helper()
	o := *orig
	t.Cleanup(func() { *orig = o })
	*orig = with
}
