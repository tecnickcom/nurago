package logutil_test

import (
	"fmt"
	"log/slog"

	"github.com/tecnickcom/nurago/pkg/logutil"
)

func ExampleNewConfig() {
	// Configuration usually comes from environment variables or a config
	// file, so the string-based options accept the raw values directly.
	cfg, err := logutil.NewConfig(
		logutil.WithFormatStr("json"),
		logutil.WithLevelStr("warning"),
		logutil.WithCommonAttr(slog.String("service", "payments")),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// SlogLogger returns a *slog.Logger writing in the configured format.
	// SlogDefaultLogger additionally installs it as the slog default.
	logger := cfg.SlogLogger()

	fmt.Println(logutil.LevelName(cfg.Level), logger != nil)

	// Output:
	// warning true
}

func ExampleWithHookFn() {
	// A hook observes every emitted record. It runs in addition to the
	// normal output, which makes it useful for counting errors or raising
	// alerts without a second logging path.
	var seen []string

	cfg, err := logutil.NewConfig(
		logutil.WithFormat(logutil.FormatNone),
		logutil.WithLevel(logutil.LevelInfo),
		logutil.WithHookFn(func(level logutil.LogLevel, message string) {
			seen = append(seen, logutil.LevelName(level)+": "+message)
		}),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	logger := cfg.SlogLogger()

	logger.Info("service started")
	logger.Error("upstream unavailable")
	logger.Debug("not emitted, below the configured level")

	for _, s := range seen {
		fmt.Println(s)
	}

	// Output:
	// info: service started
	// error: upstream unavailable
}

func ExampleParseLevel() {
	for _, name := range []string{"debug", "notice", "emergency", "nonsense"} {
		level, err := logutil.ParseLevel(name)
		fmt.Printf("%-10s %-9s %v\n", name, logutil.LevelName(level), err)
	}

	// Output:
	// debug      debug     <nil>
	// notice     notice    <nil>
	// emergency  emergency <nil>
	// nonsense   info      invalid log level "nonsense"
}
