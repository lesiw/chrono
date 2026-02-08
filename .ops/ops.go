package main

import (
	"context"
	"os"

	"labs.lesiw.io/ops/golang"
	"labs.lesiw.io/ops/golib"

	"lesiw.io/command"
	"lesiw.io/command/sys"
	"lesiw.io/ops"
)

type Ops struct{ golib.Ops }

var docker = command.Shell(sys.Machine(), "docker")

func main() {
	golang.GoModReplaceAllowed = true
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "check")
	}
	ops.Handle(Ops{})
}

func (o Ops) ExampleUp(ctx context.Context) error {
	if err := o.ExampleDown(ctx); err != nil {
		return err
	}
	ctx = command.WithEnv(ctx,
		map[string]string{"PWD": "internal/example"},
	)
	return docker.Exec(ctx,
		"docker", "compose", "up",
		"--remove-orphans",
		"--build",
	)
}

func (Ops) ExampleDown(ctx context.Context) error {
	ctx = command.WithEnv(ctx,
		map[string]string{"PWD": "internal/example"},
	)
	return docker.Exec(ctx,
		"docker", "compose", "down",
		"--remove-orphans",
		"--volumes",
	)
}
