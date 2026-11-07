package httpserver

import (
	"log/slog"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"
)

func TestWithRouter(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithRouter(nil)(cfg)
	require.Error(t, err)

	v := httprouter.New()
	err = WithRouter(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.router).Pointer())
}

func TestWithServerAddr(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerAddr("::")(cfg)
	require.Error(t, err)

	v := ":1234"
	err = WithServerAddr(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverAddr)
}

func TestWithRequestTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithRequestTimeout(-1)(cfg)
	require.Error(t, err)

	v := 3 * time.Minute
	err = WithRequestTimeout(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.requestTimeout)
}

func TestWithServerReadHeaderTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerReadHeaderTimeout(-1)(cfg)
	require.Error(t, err)

	v := 7 * time.Second
	err = WithServerReadHeaderTimeout(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverReadHeaderTimeout)
}

func TestWithServerReadTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerReadTimeout(-1)(cfg)
	require.Error(t, err)

	v := 13 * time.Second
	err = WithServerReadTimeout(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverReadTimeout)
}

func TestWithServerWriteTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerWriteTimeout(-1)(cfg)
	require.Error(t, err)

	v := 17 * time.Second
	err = WithServerWriteTimeout(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverWriteTimeout)
}

func TestWithServerIdleTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerIdleTimeout(-1)(cfg)
	require.Error(t, err)

	v := 23 * time.Second
	err = WithServerIdleTimeout(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverIdleTimeout)
}

func TestWithMaxRequestBodyBytes(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithMaxRequestBodyBytes(0)(cfg)
	require.Error(t, err)

	err = WithMaxRequestBodyBytes(-1)(cfg)
	require.Error(t, err)

	v := int64(1 << 20)
	err = WithMaxRequestBodyBytes(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.maxRequestBodyBytes)
}

func TestWithServerMaxHeaderBytes(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithServerMaxHeaderBytes(0)(cfg)
	require.Error(t, err)

	err = WithServerMaxHeaderBytes(-1)(cfg)
	require.Error(t, err)

	v := 1 << 16
	err = WithServerMaxHeaderBytes(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.serverMaxHeaderBytes)
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithShutdownTimeout(19 * time.Second)(cfg)
	require.NoError(t, err)
	require.Len(t, cfg.lifecycleOpts, 1)
}

func TestWithShutdownWaitGroup(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithShutdownWaitGroup(&sync.WaitGroup{})(cfg)
	require.NoError(t, err)
	require.Len(t, cfg.lifecycleOpts, 1)
}

func TestWithShutdownSignalChan(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithShutdownSignalChan(make(chan struct{}))(cfg)
	require.NoError(t, err)
	require.Len(t, cfg.lifecycleOpts, 1)
}

func TestWithTLSCertData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		certData []byte
		keyData  []byte
		wantErr  bool
	}{
		{
			desc:     "should fail with invalid certificate data",
			certData: []byte(""),
			keyData:  []byte(""),
			wantErr:  true,
		},
		{
			desc: "should succeed with valid certificate data",
			certData: []byte(`-----BEGIN CERTIFICATE-----
MIICBjCCAW8CFB9PJprToZgFfDJpt3Qk6JIEaMEEMA0GCSqGSIb3DQEBCwUAMEIx
CzAJBgNVBAYTAlhYMRUwEwYDVQQHDAxEZWZhdWx0IENpdHkxHDAaBgNVBAoME0Rl
ZmF1bHQgQ29tcGFueSBMdGQwHhcNMjAwNzIyMTMyMTExWhcNMzAwNzIwMTMyMTEx
WjBCMQswCQYDVQQGEwJYWDEVMBMGA1UEBwwMRGVmYXVsdCBDaXR5MRwwGgYDVQQK
DBNEZWZhdWx0IENvbXBhbnkgTHRkMIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKB
gQDTHo34VDfPXuDR4mDPpfh8hvja8loIB60b/qvv81TnJEyjLRzaI4dXclFZwUWC
zWi6LxgVcpILMG4n2KieK4h22EsaQZ7ncZ6pLTHlNJfQXWcHzUmwbA1CNyxJN72Q
LLLE3yw8Xm5AM4QegPJQ3+I27GTnAocygqVKX+aU8rUdgQIDAQABMA0GCSqGSIb3
DQEBCwUAA4GBAE3CSgcBH2P2Y0vvjyijavSCIyvau3ex1cmmybZBDen9aGhw34X5
iotTHm8vUEMtinenht11ypQhxefAreTg0RjsZuCzHlgOQrUIpY5qNSTBNTChbU/b
V6QQpxzrYshYcFuiGxSAdZMa8AFVB4Wan7Ji+vvDTJOyXbDqxA3kLFLi
-----END CERTIFICATE-----`),
			keyData: []byte(`-----BEGIN PRIVATE KEY-----
MIICdQIBADANBgkqhkiG9w0BAQEFAASCAl8wggJbAgEAAoGBANMejfhUN89e4NHi
YM+l+HyG+NryWggHrRv+q+/zVOckTKMtHNojh1dyUVnBRYLNaLovGBVykgswbifY
qJ4riHbYSxpBnudxnqktMeU0l9BdZwfNSbBsDUI3LEk3vZAsssTfLDxebkAzhB6A
8lDf4jbsZOcChzKCpUpf5pTytR2BAgMBAAECgYBPSNZAQECFXDhKGh4JXWcoPPgQ
IZu2EEvui4G+pz9nXrZ5QWPoeBdHu+LZNkAIk2OVKEJ/K3u1QAbeZ/tLC0Y/zGmS
Nv0wgCQ+A4FfQH6l5Hh3jrxFDgbjv+Lrb3Np52AC/NIU0DamNK0VffM/kZpj6Gl0
6uUtqwZwh57rJXjMkQJBAPub3EyG1p3/2CEMm2B7jmn5S+qXKgNdA681mvHY2Q6u
hhtIVtKgEV/yTvx4U6JqD1EAm8MpjfqcGHKqXIqJLn8CQQDWzct+hh5AXrirSz7o
j4WxtWuYRDr+2BWFRee0s5CaWy0y7L3fOv+RwbfFSmBgsGPSq+zXKcvOGU0S5Oca
87P/AkAxinbN+p63bXC40SqmzK014Ig6IJl9IAthrERd6jySz3pIVO4DetDw+1zi
CS8ug4OQh3Yj70KtXZ7StQiTnn8xAkBgE4I+YDytq/BLZYeIu5Ef8DZkz7fXfsz5
ZFAD6gD2mWt5CJzQePIQvqW0z9SVyq+Lbiyr/FzVHUn09n9L9c7/AkA1VDTPiY/H
DSk+QcX0L58Fc7RiaBnykcJRfHnd15MlyqtUJ02iitNJOoSVBNQzr59Iyt7nGBzm
YlAqGKDZ+A+l
-----END PRIVATE KEY-----`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			cfg := defaultConfig()
			err := WithTLSCertData(tt.certData, tt.keyData)(cfg)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, cfg.tlsConfig)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cfg.tlsConfig)
			}
		})
	}
}

func TestWithEnableDefaultRoutes(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	err := WithEnableDefaultRoutes(IndexRoute, MetricsRoute)(cfg)
	require.NoError(t, err)
	require.Equal(t, []DefaultRoute{IndexRoute, MetricsRoute}, cfg.defaultEnabledRoutes)
}

func TestWithEnableAllDefaultRoutes(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()
	err := WithEnableAllDefaultRoutes()(cfg)
	require.NoError(t, err)
	require.Equal(t, allDefaultRoutes(), cfg.defaultEnabledRoutes)
}

func TestWithIndexHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithIndexHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ []Route) http.HandlerFunc {
		return func(_ http.ResponseWriter, _ *http.Request) {
			// mock function
		}
	}
	err = WithIndexHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.indexHandlerFunc).Pointer())
}

func TestWithIPHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithIPHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithIPHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.ipHandlerFunc).Pointer())
}

func TestWithMetricsHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithMetricsHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithMetricsHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.metricsHandlerFunc).Pointer())
}

func TestWithPingHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithPingHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithPingHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.pingHandlerFunc).Pointer())
}

func TestWithPProfHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithPProfHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithPProfHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.pprofHandlerFunc).Pointer())
}

func TestWithStatusHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithStatusHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithStatusHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.statusHandlerFunc).Pointer())
}

func TestWithTraceIDHeaderName(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithTraceIDHeaderName("")(cfg)
	require.Error(t, err)

	v := "X-Test-Header"
	err = WithTraceIDHeaderName(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.traceIDHeaderName)
}

func TestWithRedactFn(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithRedactFn(nil)(cfg)
	require.Error(t, err)

	v := func(b []byte) string { return string(b) + "test" }
	err = WithRedactFn(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, "alphatest", cfg.redactFn([]byte("alpha")))
}

func TestWithMiddlewareFn(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	v := func(_ MiddlewareArgs, handler http.Handler) http.Handler { return handler }
	w := []MiddlewareFn{v, v}
	err := WithMiddlewareFn(w...)(cfg)
	require.NoError(t, err)
	require.Len(t, cfg.middleware, 2)
}

func TestWithNotFoundHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithNotFoundHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithNotFoundHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.notFoundHandlerFunc).Pointer())
}

func TestWithMethodNotAllowedHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithMethodNotAllowedHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithMethodNotAllowedHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.methodNotAllowedHandlerFunc).Pointer())
}

func TestWithPanicHandlerFunc(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithPanicHandlerFunc(nil)(cfg)
	require.Error(t, err)

	v := func(_ http.ResponseWriter, _ *http.Request) {
		// mock function
	}
	err = WithPanicHandlerFunc(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(v).Pointer(), reflect.ValueOf(cfg.panicHandlerFunc).Pointer())
}

func TestWithoutRouteLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithoutRouteLogger()(cfg)
	require.NoError(t, err)
	require.True(t, cfg.disableRouteLogger)
}

func TestWithoutDefaultRouteLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithoutDefaultRouteLogger(PingRoute, PprofRoute)(cfg)
	require.NoError(t, err)

	v, ok := cfg.disableDefaultRouteLogger[PingRoute]
	require.True(t, ok)
	require.True(t, v)

	v, ok = cfg.disableDefaultRouteLogger[PprofRoute]
	require.True(t, ok)
	require.True(t, v)
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	cfg := defaultConfig()

	err := WithLogger(nil)(cfg)
	require.Error(t, err)

	v := slog.Default()

	err = WithLogger(v)(cfg)
	require.NoError(t, err)
	require.Equal(t, v, cfg.logger)
}
