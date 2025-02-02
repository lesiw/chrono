package main

import (
	"os"

	"labs.lesiw.io/ops/golib"
	"lesiw.io/cmdio/sys"
	"lesiw.io/ops"
)

type Ops struct{ golib.Ops }

var examplernr = sys.Runner().WithEnv(map[string]string{
	"PWD": "internal/example",
})

func main() {
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "check")
	}
	ops.Handle(Ops{})
}

func (o Ops) ExampleUp() {
	o.ExampleDown()
	examplernr.MustRun(
		"docker", "compose", "up",
		"--remove-orphans",
		"--build",
	)
}

func (Ops) ExampleDown() {
	examplernr.MustRun(
		"docker", "compose", "down",
		"--remove-orphans",
		"--volumes",
	)
}
