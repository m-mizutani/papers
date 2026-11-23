package cli_test

import (
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
