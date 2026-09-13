package main

// For Postgres use the driver:
// _ "github.com/jackc/pgx/v5/stdlib"

import (
	"log/slog"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"github.com/nuragoexampleowner/nuragoexample/internal/cli"
	"github.com/tecnickcom/nurago/pkg/bootstrap"
	"github.com/tecnickcom/nurago/pkg/logsrv"
	"github.com/tecnickcom/nurago/pkg/logutil"
)

var (
	// programVersion contains the version of the application injected at compile time.
	programVersion = "0.0.0" //nolint:gochecknoglobals

	// programRelease contains the release of the application injected at compile time.
	programRelease = "0" //nolint:gochecknoglobals
)

// exitFn defines the exit function and can be overwritten for testing.
var exitFn = os.Exit //nolint:gochecknoglobals

// main initializes a safe default logger, builds the root CLI command, and
// executes it as the process entry point.
//
// Startup and command execution failures are reported with structured context
// and explicit exit codes.
func main() {
	// set default logger
	logattr := []logutil.Attr{
		slog.String("program", cli.AppName),
		slog.String("version", programVersion),
		slog.String("release", programRelease),
	}
	// NewConfig only fails when an option is invalid. Every option here is static
	// and valid, so the error cannot occur and is intentionally discarded.
	logcfg, _ := logutil.NewConfig(
		logutil.WithOutWriter(os.Stderr),
		logutil.WithFormat(logutil.FormatJSON),
		logutil.WithLevel(logutil.LevelDebug),
		logutil.WithCommonAttr(logattr...),
	)
	l := logsrv.NewLogger(logcfg)

	// build the root command and execute it, logging errors (if any)
	rootCmd, err := cli.New(programVersion, programRelease, bootstrap.Bootstrap)
	if err != nil {
		l.With(slog.Any("error", err)).Error("UNABLE TO START THE PROGRAM")
		exitFn(1)
	} else {
		err = rootCmd.Execute()
		if err != nil {
			l.With(slog.Any("error", err)).Error("UNABLE TO RUN THE COMMAND")
			exitFn(2)
		}
	}
}
