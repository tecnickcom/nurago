#!/usr/bin/env bash
#
# Check that no package under pkg/ reaches an external module it must not reach.
#
# The gRPC modules are allowed only in the packages listed below, so importing
# any other package never pulls them into a build. The module set of each
# package comes from "go list -deps".

set -euo pipefail

MODULE="github.com/tecnickcom/nurago"

# Packages allowed to reach google.golang.org/grpc. metrics/opentel reaches it
# through the OTLP exporter.
GRPC_ALLOWED="grpcclient grpcserver grpcutil metrics/opentel metrics/opentel/opentelgrpc metrics/prometheus/prometheusgrpc"

# Packages allowed to reach the go-grpc-middleware modules.
GRPCMW_ALLOWED="metrics/prometheus/prometheusgrpc"

# Packages allowed to reach the otelgrpc module.
OTELGRPC_ALLOWED="metrics/opentel/opentelgrpc"

# Packages that must reach no external module.
STDLIB_ONLY="healthcheck/healthrun srvlifecycle"

# Packages that must not reach httprouter.
NO_HTTPROUTER="grpcclient grpcserver grpcutil"

# allowed reports whether the package name is in the space-separated list.
allowed() {
	case " $2 " in
	*" $1 "*) return 0 ;;
	*) return 1 ;;
	esac
}

fail=0

for imp in $(go list ./pkg/...); do
	name="${imp#"$MODULE"/pkg/}"
	mods=$(go list -deps -f '{{if .Module}}{{.Module.Path}}{{end}}' "$imp" |
		{ grep -v -e "^$MODULE$" -e '^$' || true; } | sort -u)

	if grep -qx 'google.golang.org/grpc' <<<"$mods" && ! allowed "$name" "$GRPC_ALLOWED"; then
		echo "ERROR: $name reaches google.golang.org/grpc"
		fail=1
	fi

	if grep -q '^github.com/grpc-ecosystem/go-grpc-middleware' <<<"$mods" && ! allowed "$name" "$GRPCMW_ALLOWED"; then
		echo "ERROR: $name reaches github.com/grpc-ecosystem/go-grpc-middleware"
		fail=1
	fi

	if grep -q '/otelgrpc$' <<<"$mods" && ! allowed "$name" "$OTELGRPC_ALLOWED"; then
		echo "ERROR: $name reaches otelgrpc"
		fail=1
	fi

	if [[ -n "$mods" ]] && allowed "$name" "$STDLIB_ONLY"; then
		echo "ERROR: $name must reach no external module, but reaches: $(tr '\n' ' ' <<<"$mods")"
		fail=1
	fi

	if grep -qx 'github.com/julienschmidt/httprouter' <<<"$mods" && allowed "$name" "$NO_HTTPROUTER"; then
		echo "ERROR: $name reaches github.com/julienschmidt/httprouter"
		fail=1
	fi
done

if [[ "$fail" -ne 0 ]]; then
	echo "The dependency footprint check failed."
	exit 1
fi

echo "The dependency footprint check passed."
