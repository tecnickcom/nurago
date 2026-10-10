// Build tools for the nurago repository, declared in a separate module so that
// they stay out of the module graph of anything importing nurago.
// Installed into target/binutil by "make gotools".
module github.com/tecnickcom/nurago/resources/tools

go 1.26.0

toolchain go1.27.2

tool (
	github.com/jstemmer/go-junit-report/v2
	go.uber.org/mock/mockgen
	golang.org/x/perf/cmd/benchstat
	golang.org/x/vuln/cmd/govulncheck
)

require (
	github.com/aclements/go-moremath v0.0.0-20241023150245-c8bbc672ef66 // indirect
	github.com/jstemmer/go-junit-report/v2 v2.1.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/perf v0.0.0-20261009192801-be2c69fb417e // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20261009151841-97e8ea77aa2a // indirect
	golang.org/x/tools v0.51.0 // indirect
	golang.org/x/vuln v1.8.0 // indirect
)
