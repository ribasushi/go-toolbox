package fcli

import (
	"github.com/urfave/cli/v2"
	"github.com/urfave/cli/v2/altsrc"
)

type App = cli.App               //nolint:revive
type Context = cli.Context       //nolint:revive
type Command = cli.Command       //nolint:revive
type Flag = cli.Flag             //nolint:revive
type BoolFlag = cli.BoolFlag     //nolint:revive
type IntFlag = cli.IntFlag       //nolint:revive
type UintFlag = cli.UintFlag     //nolint:revive
type StringFlag = cli.StringFlag //nolint:revive

var ConfStringFlag = altsrc.NewStringFlag //nolint:revive
