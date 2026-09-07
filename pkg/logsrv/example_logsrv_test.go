package logsrv_test

import (
	"bytes"
	"fmt"
	"log/slog"

	"github.com/tecnickcom/nurago/pkg/logsrv"
	"github.com/tecnickcom/nurago/pkg/logutil"
	"github.com/tecnickcom/nurago/pkg/testutil"
)

func ExampleNewLogger() {
	// A real service writes to os.Stderr. The buffer keeps the example
	// output inspectable.
	var buf bytes.Buffer

	cfg, err := logutil.NewConfig(
		logutil.WithOutWriter(&buf),
		logutil.WithFormatStr("json"),
		logutil.WithLevelStr("info"),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// The returned logger is a standard *slog.Logger, so application code
	// depends only on log/slog and never on zerolog.
	logger := logsrv.NewLogger(cfg)

	logger.Info("user logged in", "user", "alice")
	logger.Debug("not emitted, below the configured level")

	// The timestamp is replaced so the example output is deterministic.
	fmt.Print(testutil.ReplaceDateTime(buf.String(), "TIME"))

	// Output:
	// {"level":"info","time":"TIME","user":"alice","trace_id":"","message":"user logged in"}
}

func ExampleNewHandler() {
	var buf bytes.Buffer

	cfg, err := logutil.NewConfig(
		logutil.WithOutWriter(&buf),
		logutil.WithFormatStr("json"),
		logutil.WithLevelStr("warning"),
	)
	if err != nil {
		fmt.Println(err)

		return
	}

	// NewHandler returns the slog.Handler on its own, for wrapping with
	// other handlers or for calling slog.New at the call site.
	handler := logsrv.NewHandler(cfg)
	logger := slog.New(handler)

	logger.Warn("disk usage above threshold", "percent", 91)
	logger.Info("not emitted, below the configured level")

	fmt.Print(testutil.ReplaceDateTime(buf.String(), "TIME"))

	// Output:
	// {"level":"warning","time":"TIME","percent":91,"trace_id":"","message":"disk usage above threshold"}
}
