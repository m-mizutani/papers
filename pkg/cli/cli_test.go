package cli_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"
	"github.com/urfave/cli/v3"

	pcli "github.com/m-mizutani/papers/pkg/cli"
)

// envVars lists the environment variables a flag reads.
func envVars(f cli.Flag) []string {
	if e, ok := f.(interface{ GetEnvVars() []string }); ok {
		return e.GetEnvVars()
	}
	return nil
}

func TestEveryFlagHasEnvironmentVariable(t *testing.T) {
	var walk func(path string, c *cli.Command)
	seen := 0
	walk = func(path string, c *cli.Command) {
		for _, f := range c.Flags {
			name := f.Names()[0]
			if name == "help" || name == "version" {
				continue
			}
			seen++
			ok := false
			for _, e := range envVars(f) {
				if strings.HasPrefix(e, "PAPERS_") {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%s --%s has no PAPERS_* environment variable", path, name)
			}
		}
		for _, sub := range c.Commands {
			walk(path+" "+sub.Name, sub)
		}
	}
	walk("papers", pcli.NewAppForTest())
	gt.N(t, seen).Greater(40)
}

func TestServeHasNoDemoFlag(t *testing.T) {
	// T1: the sample papers moved to firestore reset --demo.
	var serve *cli.Command
	for _, c := range pcli.NewAppForTest().Commands {
		if c.Name == "serve" {
			serve = c
		}
	}
	gt.V(t, serve).NotNil().Required()
	for _, f := range serve.Flags {
		for _, n := range f.Names() {
			gt.V(t, n).NotEqual("demo")
		}
		for _, e := range envVars(f) {
			gt.V(t, e).NotEqual("PAPERS_DEMO")
		}
	}

	// T2: --demo is an unknown flag of serve.
	app := pcli.NewAppForTest()
	app.Writer, app.ErrWriter = io.Discard, io.Discard
	err := app.Run(context.Background(), []string{"papers", "serve", "--demo"})
	gt.Error(t, err)
	gt.S(t, err.Error()).Contains("demo")
}
