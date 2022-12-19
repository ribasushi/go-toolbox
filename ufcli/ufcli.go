package ufcli //nolint:revive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"sync"
	"time"

	fslock "github.com/ipfs/go-fs-lock"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mattn/go-isatty"
	"github.com/prometheus/client_golang/prometheus"
	prometheuspush "github.com/prometheus/client_golang/prometheus/push"
	"github.com/ribasushi/go-toolbox/cmn"
	"github.com/urfave/cli/v2"
	"github.com/urfave/cli/v2/altsrc"
	"golang.org/x/sys/unix"
)

// nolint:revive
type Logger = *logging.ZapEventLogger // FIXME make it an actual interface

// UFcli is a urfavecli/v2/cli.App wrapper with simplified error and signal
// handling. It also provides correct init/shutdown hookpoints, and proper
// locking preventing the same app/command from running more than once.
type UFcli struct {
	AppConfig      cli.App                                                                     // stock urfavecli App configuration
	TOMLPath       string                                                                      // path of TOML config file read via https://pkg.go.dev/github.com/urfave/cli/v2/altsrc
	GlobalInit     func(cctx *cli.Context, uf *UFcli) (resourceCloser func() error, err error) // optional initialization routines (setup RDBMS pool, etc)
	BeforeShutdown func() error                                                                // optional function to execute before the top context is cancelled ( unlike resourceCloser above )
	HandleSignals  []os.Signal                                                                 // if empty defaults to DefaultHandledSignals
	Logger         Logger
}

// nolint:revive
var DefaultHandledSignals = []os.Signal{
	unix.SIGTERM,
	unix.SIGINT,
	unix.SIGHUP,
	unix.SIGPIPE,
}

// RunAndExit will excute any init routines, run the app, and os.Exit() after shutdown
func (uf *UFcli) RunAndExit(parentCtx context.Context) {
	ctx, topCtxShutdown := context.WithCancel(parentCtx)

	var resourcesCloser func() error
	var o sync.Once
	// called from the defer below
	shutdown := func(isNormal bool) {
		o.Do(func() {

			if uf.BeforeShutdown != nil {
				if err := uf.BeforeShutdown(); err != nil {
					uf.Logger.Warnf("error encountered during before-shutdown cleanup: %+v", err)
				}
			}

			topCtxShutdown()

			if resourcesCloser != nil {
				if err := resourcesCloser(); err != nil {
					uf.Logger.Warnf("error encountered during after-shutdown cleanup: %+v", err)
				}
			}

			if !isNormal {
				time.Sleep(250 * time.Millisecond) // give a bit of extra time for various parts to close
			}
		})
	}

	go func() {
		handle := uf.HandleSignals
		if len(handle) == 0 {
			handle = DefaultHandledSignals
		}
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, handle...)
		<-sigs
		uf.Logger.Warn("termination signal received, cleaning up...")
		shutdown(false)
	}()

	// BIZARRE inverted flow because... scoping
	var (
		startTime      time.Time
		scopeErr       error
		currentCmd     string
		currentCmdLock io.Closer
		promPushConf   struct {
			url      string
			user     string
			pass     string
			instance string
		}
	)
	emitEndLogs := func(wasSuccess bool) {
		// we never managed to grab a lock => we never issued BEGIN => thus no FINISH
		if currentCmdLock == nil {
			return
		}

		took := time.Since(startTime).Truncate(time.Millisecond)
		logHdr := fmt.Sprintf("=== FINISH '%s' run", currentCmd)
		logArgs := []interface{}{
			"success", wasSuccess,
			"took", took.String(),
		}

		cmdFqName := promStr(uf.AppConfig.Name + "_" + currentCmd)
		tookGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: fmt.Sprintf("%s_run_time", cmdFqName),
			Help: "How long did the job take (in milliseconds)",
		})
		tookGauge.Set(float64(took.Milliseconds()))
		successGauge := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: fmt.Sprintf("%s_success", cmdFqName),
			Help: "Whether the job completed with success(1) or failure(0)",
		})

		if wasSuccess {
			uf.Logger.Infow(logHdr, logArgs...)
			successGauge.Set(1)
		} else {
			uf.Logger.Warnw(logHdr, logArgs...)
			successGauge.Set(0)
		}

		if promPushConf.url != "" {
			p := prometheuspush.New(promPushConf.url, promStr(currentCmd))
			if promPushConf.instance != "" {
				p = p.Grouping("instance", promStr(promPushConf.instance))
			}
			if promPushConf.user != "" {
				p = p.BasicAuth(promPushConf.user, promPushConf.pass)
			}
			if promErr := p.Collector(tookGauge).Collector(successGauge).Push(); promErr != nil {
				uf.Logger.Warnf("push of prometheus metrics to '%s' failed: %s", promPushConf.url, promErr)
			}
		}
	}
	// end BIZARRE

	// a defer to always capture endstate/send a metric, even under panic()s
	defer func() {

		// a panic condition takes precedence
		if r := recover(); r != nil {
			if scopeErr == nil {
				scopeErr = fmt.Errorf("panic encountered: %s\n%s", r, debug.Stack())
			} else {
				scopeErr = fmt.Errorf("panic encountered (in addition to error '%s'): %s\n%s", scopeErr, r, debug.Stack())
			}
		}

		if scopeErr != nil {
			// if we are not interactive - be quiet on a failed lock
			if errors.As(scopeErr, new(fslock.LockedError)) && !isatty.IsTerminal(os.Stderr.Fd()) {
				shutdown(true)
				os.Exit(1)
			}

			uf.Logger.Errorf("%+v", scopeErr)
			emitEndLogs(false)
			shutdown(false)
			os.Exit(1)
		}

		emitEndLogs(true)
		shutdown(true)
		os.Exit(0)
	}()

	startTime = time.Now()

	app := uf.AppConfig
	app.ExitErrHandler = func(*cli.Context, error) {}

	for _, s := range []string{
		"prometheus_push_url",
		"prometheus_push_user",
		"prometheus_push_pass",
		"prometheus_instance",
	} {
		app.Flags = append(app.Flags, ConfStringFlag(&cli.StringFlag{
			Name:        s,
			DefaultText: "  {{ private, read from config file }}  ",
			Hidden:      true,
		}))
	}
	app.Before = func(cctx *cli.Context) error {

		// when using lp2p the first is non-actionable and
		// the second will fire arbitrarily driven by rand()
		// there is no value doing so in mainly-CLI setting
		// https://github.com/libp2p/go-libp2p/blob/master/core/canonicallog/canonicallog.go
		logging.SetLogLevel("net/identify", "ERROR")  //nolint:errcheck
		logging.SetLogLevel("canonical-log", "ERROR") //nolint:errcheck

		// pull settings from config file
		if err := altsrc.InitInputSourceWithContext(
			app.Flags,
			func(*cli.Context) (altsrc.InputSourceContext, error) {
				return altsrc.NewTomlSourceFromFile(uf.TOMLPath)
			},
		)(cctx); err != nil {
			return cmn.WrErr(err)
		}

		promPushConf.url = cctx.String("prometheus_push_url")
		promPushConf.user = cctx.String("prometheus_push_user")
		promPushConf.pass = cctx.String("prometheus_push_pass")
		promPushConf.instance = cctx.String("prometheus_instance")

		// Before() is always called with the *top* cctx in place, not the final one resolved
		// Figure out what is in os.Args out-of-band
		{
			cmdNames := make(map[string]string)
			for _, c := range cctx.App.Commands {
				if c.Name == "help" {
					continue
				}
				cmdNames[c.Name] = c.Name
				for _, a := range c.Aliases {
					cmdNames[a] = c.Name
				}
			}

			// process os.Args even if there are no cmdNames: flush out subcmd help
			for i := 1; i < len(os.Args); i++ {

				// if we are in help context - no locks and no start/stop timers
				if os.Args[i] == `-h` || os.Args[i] == `--help` {
					return nil
				}

				if currentCmd != "" {
					continue
				}
				currentCmd = cmdNames[os.Args[i]]
			}

			// not everything has subcommands
			if len(cmdNames) == 0 {
				currentCmd = "Action"
			}

			// wrong cmd or something
			if currentCmd == "" {
				return nil
			}
		}

		var err error
		if currentCmdLock, err = fslock.Lock(
			os.TempDir(),
			promStr(app.Name)+"-"+promStr(currentCmd), // reuse promstr as path-safe stuff
		); err != nil {
			return err // no xerrors wrap on purpose
		}

		uf.Logger.Infow(fmt.Sprintf("=== BEGIN '%s' run", currentCmd))

		if uf.GlobalInit != nil {
			resourcesCloser, err = uf.GlobalInit(cctx, uf)
		}
		return cmn.WrErr(err)
	}

	// the function ends after this block, scopeErr is examined in the defer above
	// organized in this bizarre way in order to catch panics
	scopeErr = (&app).RunContext(ctx, os.Args)
}

var nonAlphanumericRun = regexp.MustCompile(`[^a-zA-Z0-9]+`) //nolint:revive
func promStr(s string) string {
	return nonAlphanumericRun.ReplaceAllString(s, "_")
}
