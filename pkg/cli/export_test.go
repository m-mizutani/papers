package cli

import "github.com/urfave/cli/v3"

func NewAppForTest() *cli.Command { return newApp("test") }
