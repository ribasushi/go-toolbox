package ufcli

import (
	"github.com/urfave/cli/v2"
	"github.com/urfave/cli/v2/altsrc"
)

//nolint:revive
type (
	App        = cli.App
	Context    = cli.Context
	Command    = cli.Command
	Flag       = cli.Flag
	BoolFlag   = cli.BoolFlag
	IntFlag    = cli.IntFlag
	UintFlag   = cli.UintFlag
	StringFlag = cli.StringFlag
)

//nolint:revive
var ConfStringFlag = altsrc.NewStringFlag
