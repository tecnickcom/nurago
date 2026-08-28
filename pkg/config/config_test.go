//go:generate go tool mockgen -write_package_comment=false -package config -destination ./mock_test.go . Viper

package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type testData struct {
	Str string `mapstructure:"str"`
	Int int    `mapstructure:"int"`
	Arr []int  `mapstructure:"arr"`
}

type testConfig struct {
	BaseConfig `mapstructure:",squash"`

	Data        testData `mapstructure:"data"`
	Slice       []string `mapstructure:"slice"`
	validateErr error
	String      string                       `mapstructure:"string"`
	Int         int                          `mapstructure:"int"`
	Int64       int64                        `mapstructure:"int64"`
	UInt        uint                         `mapstructure:"uint"`
	UInt64      uint64                       `mapstructure:"uint64"`
	MapString   map[string]string            `mapstructure:"mapstring"`
	NestedMap   map[string]map[string]string `mapstructure:"nestedmap"`
	MapData     map[string]testData          `mapstructure:"mapdata"`
	Int32       int32                        `mapstructure:"int32"`
	UInt32      uint32                       `mapstructure:"uint32"`
	Float32     float32                      `mapstructure:"float32"`
	Float64     float64                      `mapstructure:"float64"`
	Int16       int16                        `mapstructure:"int16"`
	UInt16      uint16                       `mapstructure:"uint16"`
	Boolean     bool                         `mapstructure:"bool"`
	Int8        int8                         `mapstructure:"int8"`
	UInt8       uint8                        `mapstructure:"uint8"`
}

func (tc *testConfig) SetDefaults(v Viper) {
	v.SetDefault("alpha", "beta")
}

func (tc *testConfig) Validate() error {
	return tc.validateErr
}

// nopRemoteLoader is a no-op remote loader used to exercise code paths that
// only check for the presence of a registered loader.
func nopRemoteLoader(_ *RemoteSourceConfig) (io.Reader, error) {
	return strings.NewReader(`{}`), nil
}

func Test_configureConfigSearchPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configDir  string
		setupMocks func(m *MockViper)
	}{
		{
			name: "without config dir",
			setupMocks: func(m *MockViper) {
				m.EXPECT().AddConfigPath("./")
				m.EXPECT().AddConfigPath("$HOME/.test/")
				m.EXPECT().AddConfigPath("/etc/test/")
			},
		},
		{
			name:      "with config dir",
			configDir: "/config_source_test/",
			setupMocks: func(m *MockViper) {
				m.EXPECT().AddConfigPath("/config_source_test/")
				m.EXPECT().AddConfigPath("./")
				m.EXPECT().AddConfigPath("$HOME/.test/")
				m.EXPECT().AddConfigPath("/etc/test/")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)

			defer ctrl.Finish()

			tmv := NewMockViper(ctrl)
			if tt.setupMocks != nil {
				tt.setupMocks(tmv)
			}

			configureSearchPath(tmv, "test", tt.configDir)
		})
	}
}

func mockViper(ctrl *gomock.Controller) *MockViper {
	mock := NewMockViper(ctrl)

	mock.EXPECT().SetConfigName(defaultConfigName)
	mock.EXPECT().SetConfigType(defaultConfigType)

	mock.EXPECT().SetDefault(keyRemoteConfigProvider, defaultRemoteConfigProvider)
	mock.EXPECT().SetDefault(keyRemoteConfigEndpoint, defaultRemoteConfigEndpoint)
	mock.EXPECT().SetDefault(keyRemoteConfigPath, defaultRemoteConfigPath)
	mock.EXPECT().SetDefault(keyRemoteConfigSecretKeyring, defaultRemoteConfigSecretKeyring)
	mock.EXPECT().SetDefault(keyRemoteConfigData, defaultRemoteConfigData)

	mock.EXPECT().SetDefault(keyLogFormat, defaultLogFormat)
	mock.EXPECT().SetDefault(keyLogLevel, defaultLogLevel)
	mock.EXPECT().SetDefault(keyLogAddress, defaultLogAddress)
	mock.EXPECT().SetDefault(keyLogNetwork, defaultLogNetwork)

	mock.EXPECT().SetDefault("shutdown_timeout", defaultShutdownTimeout)

	mock.EXPECT().SetDefault("alpha", "beta")

	mock.EXPECT().AddConfigPath(gomock.Any()).AnyTimes()
	mock.EXPECT().AutomaticEnv()
	mock.EXPECT().SetEnvPrefix("test")
	mock.EXPECT().BindEnv(gomock.Any()).AnyTimes()

	return mock
}

func Test_loadLocalConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		configContent  []byte
		envDataContent []byte
		setupViper     func(ctrl *gomock.Controller) Viper
		remoteLoader   RemoteLoaderFunc
		want           *RemoteSourceConfig
		wantErr        bool
	}{
		{
			name: "fails with read config error",
			setupViper: func(ctrl *gomock.Controller) Viper {
				mock := mockViper(ctrl)
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().ReadInConfig().Return(errors.New("read config error"))

				return mock
			},
			wantErr: true,
		},
		{
			name: "fails with unmarshal error",
			setupViper: func(ctrl *gomock.Controller) Viper {
				mock := mockViper(ctrl)
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().ReadInConfig()
				mock.EXPECT().Unmarshal(gomock.Any()).Return(errors.New("unmarshal error"))

				return mock
			},
			wantErr: true,
		},
		{
			name: "succeed",
			setupViper: func(ctrl *gomock.Controller) Viper {
				mock := mockViper(ctrl)
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().ReadInConfig()
				mock.EXPECT().Unmarshal(gomock.Any())

				return mock
			},
			want:    &RemoteSourceConfig{},
			wantErr: false,
		},
		{
			name: "succeed when the local config file is not found",
			setupViper: func(ctrl *gomock.Controller) Viper {
				mock := mockViper(ctrl)
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().ReadInConfig().Return(viper.ConfigFileNotFoundError{})
				mock.EXPECT().Unmarshal(gomock.Any())

				return mock
			},
			want:    &RemoteSourceConfig{},
			wantErr: false,
		},
		{
			name:          "succeed from empty file",
			configContent: []byte(`{}`),
			setupViper: func(_ *gomock.Controller) Viper {
				return viper.New()
			},
			want: &RemoteSourceConfig{
				Provider:      defaultRemoteConfigProvider,
				Endpoint:      defaultRemoteConfigEndpoint,
				Path:          defaultRemoteConfigPath,
				SecretKeyring: defaultRemoteConfigSecretKeyring,
				Data:          "",
			},
			wantErr: false,
		},
		{
			name: "succeed from populated file",
			configContent: []byte(`
{
  "remoteConfigProvider": "envvar",
  "remoteConfigData": "eyJrZXkiOiJvbmUifQ=="
}`),
			setupViper: func(_ *gomock.Controller) Viper {
				return viper.New()
			},
			want: &RemoteSourceConfig{
				Provider: "envvar",
				Data:     "eyJrZXkiOiJvbmUifQ==",
			},
			wantErr: false,
		},
		{
			// F1: an unsupported provider name must fail fast at load time instead
			// of silently falling through to the remote-backend path.
			name: "fails validation with unsupported provider",
			configContent: []byte(`
{
  "remoteConfigProvider": "notaprovider"
}`),
			setupViper: func(_ *gomock.Controller) Viper {
				return viper.New()
			},
			wantErr: true,
		},
		{
			name: "succeed with custom provider when a remote loader is registered",
			configContent: []byte(`
{
  "remoteConfigProvider": "consul",
  "remoteConfigEndpoint": "remote:1234",
  "remoteConfigPath": "/config/path"
}`),
			setupViper: func(_ *gomock.Controller) Viper {
				return viper.New()
			},
			remoteLoader: nopRemoteLoader,
			want: &RemoteSourceConfig{
				Provider:      "consul",
				Endpoint:      "remote:1234",
				Path:          "/config/path",
				SecretKeyring: "",
				Data:          "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			var (
				configDir string
				err       error
			)

			if tt.configContent != nil {
				configDir = t.TempDir()

				defer func() { _ = os.RemoveAll(configDir) }()

				tmpFilePath := filepath.Join(configDir, "config.json")
				require.NoError(t, os.WriteFile(tmpFilePath, tt.configContent, 0o600), "failed writing temp config file: %v", err)
			}

			v := tt.setupViper(ctrl)

			var testCfg testConfig

			got, err := loadLocalConfig(v, "test_name", configDir, "test", &testCfg, &options{remoteLoader: tt.remoteLoader})
			if (err != nil) != tt.wantErr {
				t.Errorf("loadLocalConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadLocalConfig() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_validationError(t *testing.T) {
	t.Parallel()

	err := validationError("provider", "prefix", "var")
	require.EqualError(t, err, "missing required remote configuration variable: provider config provider requires PREFIX_VAR to be set")
	require.ErrorIs(t, err, ErrMissingRemoteVar)

	// the "-" to "_" replacement applied when binding environment variables
	// must also be applied to the variable name printed in the error message
	err = validationError("envvar", "my-app", keyRemoteConfigData)
	require.EqualError(t, err, "missing required remote configuration variable: envvar config provider requires MY_APP_REMOTECONFIGDATA to be set")
}

// Test_validateRemoteSourceConfig verifies the imperative provider check: the
// empty value and the built-in envvar provider always pass, while any other
// provider name is accepted only when a remote loader is registered and fails
// fast with ErrInvalidRemoteConfig otherwise.
func Test_validateRemoteSourceConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		cfg             RemoteSourceConfig
		hasRemoteLoader bool
		wantErr         bool
	}{
		{name: "empty provider passes", cfg: RemoteSourceConfig{}},
		{name: "empty provider passes with loader", cfg: RemoteSourceConfig{}, hasRemoteLoader: true},
		{name: "envvar passes", cfg: RemoteSourceConfig{Provider: providerEnvVar}},
		{name: "custom provider passes with loader", cfg: RemoteSourceConfig{Provider: "consul"}, hasRemoteLoader: true},
		{name: "custom provider fails without loader", cfg: RemoteSourceConfig{Provider: "consul"}, wantErr: true},
		{name: "unsupported provider fails", cfg: RemoteSourceConfig{Provider: "notaprovider"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRemoteSourceConfig(&tt.cfg, tt.hasRemoteLoader)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidRemoteConfig)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

//nolint:gocognit
func Test_loadRemoteConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		setupConfigSource func() *RemoteSourceConfig
		setupLocalViper   func(ctrl *gomock.Controller) Viper
		setupRemoteViper  func(ctrl *gomock.Controller) Viper
		want              *testConfig
		wantErr           bool
	}{
		{
			name: "completes configuration flow",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{}
			},
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AllKeys().Return([]string{
					keyLogLevel,
				})
				mock.EXPECT().Get(keyLogLevel).Return("DEBUG")

				return mock
			},
			setupRemoteViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().SetDefault(keyLogLevel, gomock.Any())
				mock.EXPECT().SetConfigType(defaultConfigType)
				mock.EXPECT().AutomaticEnv()
				mock.EXPECT().SetEnvPrefix(gomock.Any())
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().Unmarshal(gomock.Any())

				return mock
			},
		},
		{
			name: "fails with unmarshal error",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{}
			},
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AllKeys().Return([]string{
					keyLogLevel,
				})
				mock.EXPECT().Get(keyLogLevel).Return("DEBUG")

				return mock
			},
			setupRemoteViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().SetDefault(keyLogLevel, gomock.Any())
				mock.EXPECT().SetConfigType(defaultConfigType)
				mock.EXPECT().AutomaticEnv()
				mock.EXPECT().SetEnvPrefix(gomock.Any())
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().Unmarshal(gomock.Any()).Return(errors.New("unmarshal error"))

				return mock
			},
			wantErr: true,
		},
		{
			name: "fails with load envvar error",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
				}
			},
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AllKeys().Return([]string{
					keyLogLevel,
				})
				mock.EXPECT().Get(keyLogLevel).Return("DEBUG")

				return mock
			},
			setupRemoteViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().SetDefault(keyLogLevel, gomock.Any())
				mock.EXPECT().SetConfigType(defaultConfigType)
				mock.EXPECT().AutomaticEnv()
				mock.EXPECT().SetEnvPrefix(gomock.Any())
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())

				return mock
			},
			wantErr: true,
		},
		{
			// A custom provider name reaches the defensive default branch when
			// loadRemoteConfig is called directly without a registered remote
			// loader (Load rejects it earlier).
			name: "fails with unsupported provider",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "consul",
				}
			},
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AllKeys().Return([]string{
					keyLogLevel,
				})
				mock.EXPECT().Get(keyLogLevel).Return("DEBUG")

				return mock
			},
			setupRemoteViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().SetDefault(keyLogLevel, gomock.Any())
				mock.EXPECT().SetConfigType(defaultConfigType)
				mock.EXPECT().AutomaticEnv()
				mock.EXPECT().SetEnvPrefix(gomock.Any())
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())

				return mock
			},
			wantErr: true,
		},
		{
			name: "loads a valid configuration",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{}
			},
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AllKeys().Return([]string{
					keyLogLevel,
				})
				mock.EXPECT().Get(keyLogLevel).Return("DEBUG")

				return mock
			},
			setupRemoteViper: func(_ *gomock.Controller) Viper {
				return viper.New()
			},
			want: &testConfig{
				Log: LogConfig{
					Level: "DEBUG",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			rsCfg := tt.setupConfigSource()

			lv := tt.setupLocalViper(ctrl)
			rv := tt.setupRemoteViper(ctrl)

			var testCfg testConfig

			err := loadRemoteConfig(lv, rv, rsCfg, "test", &testCfg, &options{})
			if (err != nil) != tt.wantErr {
				t.Errorf("loadRemoteConfig() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.want != nil {
				if !reflect.DeepEqual(fmt.Sprintf("%+v", &testCfg), fmt.Sprintf("%+v", tt.want)) {
					t.Errorf("loadLocalConfig() got = %+v, want %+v", &testCfg, tt.want)
				}
			}
		})
	}
}

func Test_loadFromEnvVarSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		setupConfigSource func() *RemoteSourceConfig
		setupMocks        func(mv *MockViper)
		wantErr           bool
	}{
		{
			name: "fails with missing data in configuration",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
				}
			},
			wantErr: true,
		},
		{
			name: "fail with badly encoded envvar data",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
					Data:     "AAA@",
				}
			},
			wantErr: true,
		},
		{
			name: "fail with envvar data read config error",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
					Data:     "AA==",
				}
			},
			setupMocks: func(mv *MockViper) {
				mv.EXPECT().ReadConfig(gomock.Any()).Return(errors.New("read config error"))
			},
			wantErr: true,
		},
		{
			name: "succeed with envvar data",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
					Data:     "AA==",
				}
			},
			setupMocks: func(mv *MockViper) {
				mv.EXPECT().ReadConfig(gomock.Any())
			},
			wantErr: false,
		},
		{
			name: "succeed with envvar JSON data payload",
			setupConfigSource: func() *RemoteSourceConfig {
				return &RemoteSourceConfig{
					Provider: "envvar",
					Data:     "eyJrZXkiOiJvbmUifQ==", // {"key":"one"} in base64
				}
			},
			setupMocks: func(mv *MockViper) {
				mv.EXPECT().ReadConfig(gomock.Any()).Do(func(in any) {
					// Validate that the decoded data contains the expected JSON
					var data map[string]any

					reader, ok := in.(io.Reader)

					require.True(t, ok, "expected io.Reader")

					err := json.NewDecoder(reader).Decode(&data)
					require.NoError(t, err)
					require.Equal(t, "one", data["key"])
				})
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			v := NewMockViper(ctrl)
			if tt.setupMocks != nil {
				tt.setupMocks(v)
			}

			err := loadFromEnvVarSource(v, tt.setupConfigSource(), "TESTNURAGO")
			if (err != nil) != tt.wantErr {
				t.Errorf("loadFromEnvVarSource() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_loadFromEnvVarSource_with_override(t *testing.T) {
	t.Setenv("TESTNURAGO_OVKEY", "two_from_env")

	setupConfigSource := func() *RemoteSourceConfig {
		return &RemoteSourceConfig{
			Provider: "envvar",
			Data:     "eyJvdmtleSI6Im9uZSJ9", // {"ovkey":"one"} in base64
		}
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	v := viper.New()
	v.SetConfigType("json")
	v.AutomaticEnv()
	v.SetEnvPrefix("TESTNURAGO")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	err := loadFromEnvVarSource(v, setupConfigSource(), "TESTNURAGO")
	require.NoError(t, err)

	require.Equal(t, os.Getenv("TESTNURAGO_OVKEY"), v.Get("ovkey"))
}

//nolint:gocognit,maintidx
func Test_loadConfig(t *testing.T) {
	tests := []struct {
		name             string
		setupLocalViper  func(ctrl *gomock.Controller) Viper
		setupRemoteViper func(ctrl *gomock.Controller) Viper
		configContent    []byte
		envDataContent   []byte
		targetConfig     *testConfig
		wantConfig       *testConfig
		wantErr          bool
	}{
		{
			name: "success with all types and min type values",
			configContent: []byte(`
{
  "log": {
    "format": "json",
    "level": "debug",
    "network": "log_network",
    "address": "log_address"
  },
  "bool": false,
  "string": "string_value",
  "int": -12345,
  "int8": -128,
  "int16": -32768,
  "int32": -2147483648,
  "int64": -9007199254740991,
  "uint": 0,
  "uint8": 0,
  "uint16": 0,
  "uint32": 0,
  "uint64": 0,
  "float32": -1234567.89,
  "float64": -1234567890.123,
  "slice": [
    "a",
    "b"
  ],
  "mapstring": {
    "k1": "v1",
    "k2": "v2"
  },
  "nestedmap": {
    "k1": {
      "ck1": "v1",
      "ck2": "v2"
    }
  },
  "data": {
    "str": "data_string",
    "int": 12345,
    "arr": [
      1,
      2,
      3
    ]
  },
  "mapdata": {
    "d1": {
      "str": "data_string",
      "int": 56789,
      "arr": [
        4,
        5,
        6
      ]
    }
  }
}
`),
			targetConfig: &testConfig{},
			wantConfig: &testConfig{
				Log: LogConfig{
					Format:  "json",
					Level:   "debug",
					Network: "log_network",
					Address: "log_address",
				},
				ShutdownTimeout: defaultShutdownTimeout,
				Boolean:         false,
				String:          "string_value",
				Int:             -12345,
				Int8:            -128,
				Int16:           -32768,
				Int32:           -2147483648,
				Int64:           -9007199254740991,
				UInt:            0,
				UInt8:           0,
				UInt16:          0,
				UInt32:          0,
				UInt64:          0,
				Float32:         -1234567.89,
				Float64:         -1234567890.123,
				Slice:           []string{"a", "b"},
				MapString: map[string]string{
					"k1": "v1",
					"k2": "v2",
				},
				NestedMap: map[string]map[string]string{
					"k1": {
						"ck1": "v1",
						"ck2": "v2",
					},
				},
				Data: testData{
					Str: "data_string",
					Int: 12345,
					Arr: []int{1, 2, 3},
				},
				MapData: map[string]testData{
					"d1": {
						Str: "data_string",
						Int: 56789,
						Arr: []int{4, 5, 6},
					},
				},
			},
		},
		{
			name: "success with all types and max type values",
			configContent: []byte(`
{
  "log": {
    "format": "json",
    "level": "debug",
    "network": "log_network",
    "address": "log_address"
  },
  "bool": true,
  "string": "string_value",
  "int": 12345,
  "int8": 127,
  "int16": 32767,
  "int32": 2147483647,
  "int64": 9007199254740991,
  "uint": 12345,
  "uint8": 255,
  "uint16": 65535,
  "uint32": 4294967295,
  "uint64": 9007199254740991,
  "float32": 1234567.89,
  "float64": 1234567890.123,
  "slice": [
    "a",
    "b"
  ],
  "mapstring": {
    "k1": "v1",
    "k2": "v2"
  },
  "nestedmap": {
    "k1": {
      "ck1": "v1",
      "ck2": "v2"
    }
  },
  "data": {
    "str": "data_string",
    "int": 12345,
    "arr": [
      1,
      2,
      3
    ]
  },
  "mapdata": {
    "d1": {
      "str": "data_string",
      "int": 56789,
      "arr": [
        4,
        5,
        6
      ]
    }
  }
}
`),
			targetConfig: &testConfig{},
			wantConfig: &testConfig{
				Log: LogConfig{
					Format:  "json",
					Level:   "debug",
					Network: "log_network",
					Address: "log_address",
				},
				ShutdownTimeout: defaultShutdownTimeout,
				Boolean:         true,
				String:          "string_value",
				Int:             12345,
				Int8:            127,
				Int16:           32767,
				Int32:           2147483647,
				Int64:           9007199254740991,
				UInt:            12345,
				UInt8:           255,
				UInt16:          65535,
				UInt32:          4294967295,
				UInt64:          9007199254740991,
				Float32:         1234567.89,
				Float64:         1234567890.123,
				Slice:           []string{"a", "b"},
				MapString: map[string]string{
					"k1": "v1",
					"k2": "v2",
				},
				NestedMap: map[string]map[string]string{
					"k1": {
						"ck1": "v1",
						"ck2": "v2",
					},
				},
				Data: testData{
					Str: "data_string",
					Int: 12345,
					Arr: []int{1, 2, 3},
				},
				MapData: map[string]testData{
					"d1": {
						Str: "data_string",
						Int: 56789,
						Arr: []int{4, 5, 6},
					},
				},
			},
		},
		{
			name: "fails loading local config",
			setupLocalViper: func(ctrl *gomock.Controller) Viper {
				mock := mockViper(ctrl)
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().ReadInConfig().Return(errors.New("read config error"))

				return mock
			},
			targetConfig: &testConfig{},
			wantErr:      true,
		},
		{
			name: "fails loading remote config",
			setupRemoteViper: func(ctrl *gomock.Controller) Viper {
				mock := NewMockViper(ctrl)
				mock.EXPECT().AutomaticEnv()
				mock.EXPECT().SetEnvPrefix(gomock.Any())
				mock.EXPECT().SetEnvKeyReplacer(gomock.Any())
				mock.EXPECT().SetDefault(gomock.Any(), gomock.Any()).AnyTimes()
				mock.EXPECT().SetConfigType(defaultConfigType)
				mock.EXPECT().Unmarshal(gomock.Any()).Return(errors.New("unmarshal error"))

				return mock
			},
			configContent: []byte(`
{
  "log": {
    "format": "json",
    "level": "debug",
    "network": "log_network",
    "address": "log_address"
  },
  "data": {
    "str": "data_string",
    "int": 12345,
    "arr": [
      1,
      2,
      3
    ]
  }
}
`),
			targetConfig: &testConfig{},
			wantErr:      true,
		},
		{
			name: "fails validating configuration",
			configContent: []byte(`
{
  "log": {
    "format": "json",
    "level": "debug",
    "network": "log_network",
    "address": "log_address"
  },
  "data": {
    "str": "data_string",
    "int": 12345,
    "arr": [
      1,
      2,
      3
    ]
  }
}
`),
			targetConfig: &testConfig{validateErr: errors.New("validate error")},
			wantErr:      true,
		},
		{
			name: "success load local config w/o remote override",
			configContent: []byte(`
{
  "log": {
	"format": "json",
	"level": "debug",
	"network": "log_network",
	"address": "log_address"
  },
  "data": {
	"str": "data_string",
	"int": 12345,
	"arr": [
	  1,
	  2,
	  3
	]
  }
}
`),
			targetConfig: &testConfig{},
			wantConfig: &testConfig{
				Log: LogConfig{
					Format:  "json",
					Level:   "debug",
					Network: "log_network",
					Address: "log_address",
				},
				ShutdownTimeout: defaultShutdownTimeout,
				Data: testData{
					Str: "data_string",
					Int: 12345,
					Arr: []int{1, 2, 3},
				},
			},
		},
		{
			name: "success load local config w/ envar override",
			configContent: []byte(`
{
  "remoteConfigProvider": "envvar",
  "log": {
	"format": "console",
	"level": "info"
  },
  "data": {
	"str": "data_string",
	"int": 12345,
	"arr": [
	  1,
	  2,
	  3
	]
  }
}
`),
			envDataContent: []byte(`
{
  "log": {
	"format": "json",
	"level": "debug",
	"network": "log_network",
	"address": "log_address"
  },
  "data": {
	"str": "data_string",
	"int": 56,
	"arr": [
	  4,
	  5,
	  6
	]
  }
}
`),
			targetConfig: &testConfig{},
			wantConfig: &testConfig{
				Log: LogConfig{
					Format:  "json",
					Level:   "debug",
					Network: "log_network",
					Address: "log_address",
				},
				ShutdownTimeout: defaultShutdownTimeout,
				Data: testData{
					Str: "data_string",
					Int: 56,
					Arr: []int{4, 5, 6},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			var tmpConfigDir string

			var err error

			if tt.configContent != nil {
				tmpConfigDir = t.TempDir()

				defer func() { _ = os.RemoveAll(tmpConfigDir) }()

				tmpFilePath := filepath.Join(tmpConfigDir, "config.json")
				require.NoError(t, os.WriteFile(tmpFilePath, tt.configContent, 0o600), "failed writing temp config file: %v", err)
			}

			var localViper Viper
			if tt.setupLocalViper != nil {
				localViper = tt.setupLocalViper(ctrl)
			} else {
				localViper = viper.New()
			}

			var remoteViper Viper
			if tt.setupRemoteViper != nil {
				remoteViper = tt.setupRemoteViper(ctrl)
			} else {
				remoteViper = viper.New()
			}

			if tt.envDataContent != nil {
				envKey := "TEST_REMOTECONFIGDATA"
				t.Setenv(envKey, base64.StdEncoding.EncodeToString(tt.envDataContent))
			}

			err = loadConfig(localViper, remoteViper, "cmd", tmpConfigDir, "test", tt.targetConfig, &options{})
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantConfig != nil {
				cfgStr := func() string {
					data, _ := json.Marshal(tt.targetConfig)
					return string(data)
				}()

				wantCfgStr := func() string {
					data, _ := json.Marshal(tt.wantConfig)
					return string(data)
				}()

				if cfgStr != wantCfgStr {
					t.Errorf("loadRemoteSourceConfig() got = %s, want = %s", cfgStr, wantCfgStr)
				}
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tmpConfigDir := t.TempDir()

	defer func() { _ = os.RemoveAll(tmpConfigDir) }()

	tmpFilePath := filepath.Join(tmpConfigDir, "config.json")
	configContent := []byte(`
{
  "log": {
	"format": "json",
	"level": "debug",
	"network": "log_network",
	"address": "log_address"
  },
  "data": {
	"str": "data_string",
	"int": 12345,
	"arr": [
	  1,
	  2,
	  3
	]
  }
}
`)
	require.NoError(t, os.WriteFile(tmpFilePath, configContent, 0o600), "failed writing temp config file")

	targetConfig := &testConfig{}

	err := Load("cmd", tmpConfigDir, "test", targetConfig)
	require.NoError(t, err)
}

func TestLoad_noLocalConfigFileWithEnvVarProvider(t *testing.T) {
	envData := base64.StdEncoding.EncodeToString([]byte(`{"string":"envvar_value","int":7}`))

	t.Setenv("TESTNOFILE_REMOTECONFIGPROVIDER", "envvar")
	t.Setenv("TESTNOFILE_REMOTECONFIGDATA", envData)

	targetConfig := &testConfig{}

	// The provided config directory is empty and no config.json exists in any
	// other search path, so loading must succeed purely from the built-in
	// defaults plus the envvar-provider data (fully file-less deployment).
	err := Load("nurago-config-test-nofile", t.TempDir(), "testnofile", targetConfig)
	require.NoError(t, err)
	require.Equal(t, "envvar_value", targetConfig.String)
	require.Equal(t, 7, targetConfig.Int)
	require.Equal(t, defaultLogFormat, targetConfig.Log.Format)
	require.Equal(t, int64(defaultShutdownTimeout), targetConfig.ShutdownTimeout)
}

func TestLoad_nilConfiguration(t *testing.T) {
	t.Parallel()

	err := Load("cmd", t.TempDir(), "test", nil)
	require.ErrorIs(t, err, ErrNilConfiguration)
}

// TestLoad_invalidRemoteProviderFailsFast verifies F1: an unsupported provider
// name (here injected via environment variable) is rejected by Load with a
// wrapped ErrInvalidRemoteConfig instead of silently reaching the remote backend.
// Being a local-load failure, the error also matches ErrLocalConfig.
func TestLoad_invalidRemoteProviderFailsFast(t *testing.T) {
	t.Setenv("TESTBADPROV_REMOTECONFIGPROVIDER", "notaprovider")

	err := Load("nurago-config-test-badprov", t.TempDir(), "testbadprov", &testConfig{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidRemoteConfig)
	require.ErrorIs(t, err, ErrLocalConfig)
}

// TestLoad_envOverridesRegisteredDefault verifies F3: a key that has a registered
// default (log.format) is overridable via environment variable. A key without a
// default would not be picked up (see the SetDefaults contract).
func TestLoad_envOverridesRegisteredDefault(t *testing.T) {
	t.Setenv("TESTENVOVR_LOG_FORMAT", "CONSOLE")

	targetConfig := &testConfig{}

	err := Load("nurago-config-test-envovr", t.TempDir(), "testenvovr", targetConfig)
	require.NoError(t, err)
	require.Equal(t, "CONSOLE", targetConfig.Log.Format)
}
