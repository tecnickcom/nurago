package filter

import (
	"errors"
	"math"
	"net/url"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stringAlias string

func getSliceLen(slice any) uint {
	rSlice := reflect.ValueOf(slice)
	rSlice = reflect.Indirect(rSlice)

	return uint(rSlice.Len())
}

func TestParseJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		want    [][]Rule
		wantErr bool
	}{
		{
			name: "success",
			json: `[
			  [
				{ "field": "name", "type": "==", "value": "doe" },
				{ "field": "age", "type": "<=", "value": 42 }
			  ],
			  [
				{ "field": "address.country", "type": "regexp", "value": "^EN$|^FR$" }
			  ]
			]`,
			want: [][]Rule{
				{
					{Field: "name", Type: TypeEqual, Value: "doe"},
					{Field: "age", Type: TypeLTE, Value: int64(42)},
				},
				{
					{Field: "address.country", Type: TypeRegexp, Value: "^EN$|^FR$"},
				},
			},
			wantErr: false,
		},
		{
			// The whole point of decoding with json.Number: 2^53+1 has no exact float64
			// representation, so a float64 rule value would match 2^53 as well.
			name: "success - integer beyond 2^53 stays exact",
			json: `[[{"field":"ID","type":"==","value":9007199254740993}]]`,
			want: [][]Rule{{
				{Field: "ID", Type: TypeEqual, Value: int64(9007199254740993)},
			}},
		},
		{
			name: "success - integer beyond MaxInt64 becomes uint64",
			json: `[[{"field":"ID","type":"==","value":18446744073709551615}]]`,
			want: [][]Rule{{
				{Field: "ID", Type: TypeEqual, Value: uint64(math.MaxUint64)},
			}},
		},
		{
			name: "success - negative integer",
			json: `[[{"field":"ID","type":"==","value":-9223372036854775808}]]`,
			want: [][]Rule{{
				{Field: "ID", Type: TypeEqual, Value: int64(math.MinInt64)},
			}},
		},
		{
			name: "success - fractional value stays float64",
			json: `[[{"field":"Score","type":"<","value":1.5}]]`,
			want: [][]Rule{{
				{Field: "Score", Type: TypeLT, Value: 1.5},
			}},
		},
		{
			// Exponent notation is a JSON float, so it stays a float64 even when it happens
			// to hold an integral value.
			name: "success - exponent notation stays float64",
			json: `[[{"field":"Score","type":"<","value":1e3}]]`,
			want: [][]Rule{{
				{Field: "Score", Type: TypeLT, Value: float64(1000)},
			}},
		},
		{
			name:    "error - invalid json",
			json:    `[`,
			wantErr: true,
		},
		{
			// json.Unmarshal rejects trailing data; json.Decoder ignores it unless asked.
			name:    "error - data after the rule set",
			json:    `[[{"field":"ID","type":"==","value":1}]] {"trailing":"junk"}`,
			wantErr: true,
		},
		{
			name:    "error - number no Go type can represent",
			json:    `[[{"field":"ID","type":"==","value":1e400}]]`,
			wantErr: true,
		},
		{
			name:    "error - array rule value",
			json:    `[[{"field":"ID","type":"==","value":[1,2]}]]`,
			wantErr: true,
		},
		{
			name:    "error - object rule value",
			json:    `[[{"field":"ID","type":"==","value":{"a":1}}]]`,
			wantErr: true,
		},
		{
			// The schema requires all three keys; an omitted one is a malformed filter,
			// distinct from an explicit null.
			name:    "error - missing field key",
			json:    `[[{"type":"==","value":"x"}]]`,
			wantErr: true,
		},
		{
			name:    "error - missing type key",
			json:    `[[{"field":"ID","value":"x"}]]`,
			wantErr: true,
		},
		{
			name:    "error - missing value key",
			json:    `[[{"field":"ID","type":"=="}]]`,
			wantErr: true,
		},
		{
			name:    "error - empty outer array",
			json:    `[]`,
			wantErr: true,
		},
		{
			// An explicit null value is present (distinct from an omitted key) and normalizes
			// to a nil reference.
			name: "success - explicit null value",
			json: `[[{"field":"","type":"==","value":null}]]`,
			want: [][]Rule{{
				{Field: "", Type: TypeEqual, Value: nil},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := parseJSON(tt.json)

			if tt.wantErr {
				require.ErrorIs(t, err, ErrInvalidFilter, "ParseRules() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, r, "Filtered = %v, want %v", r, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
	}{
		{
			name: "success",
			opts: []Option{
				func(_ *Processor) error {
					return nil
				},
			},
			wantErr: false,
		},
		{
			name: "error - option error",
			opts: []Option{
				func(_ *Processor) error {
					return errors.New("test error")
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New(tt.opts...)

			if tt.wantErr {
				require.Error(t, err, "New() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.NotNil(t, p)
			}
		})
	}
}

func TestFilter_ParseURLQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawQuery string
		opts     []Option
		want     [][]Rule
		wantErr  bool
	}{
		{
			// [[{"field":"Age","type":"==","value":42}]]
			name:     "success - default key",
			rawQuery: "filter=%5B%5B%7B%22field%22%3A%22Age%22%2C%22type%22%3A%22%3D%3D%22%2C%22value%22%3A42%7D%5D%5D",
			want: [][]Rule{{{
				Field: "Age",
				Type:  TypeEqual,
				Value: int64(42),
			}}},
			wantErr: false,
		},
		{
			// [[{"field":"Age","type":"==","value":42}]]
			name:     "success - custom key",
			rawQuery: "myCustomFilter=%5B%5B%7B%22field%22%3A%22Age%22%2C%22type%22%3A%22%3D%3D%22%2C%22value%22%3A42%7D%5D%5D",
			opts:     []Option{WithQueryFilterKey("myCustomFilter")},
			want: [][]Rule{{{
				Field: "Age",
				Type:  TypeEqual,
				Value: int64(42),
			}}},
			wantErr: false,
		},
		{
			name:     "success - empty value",
			rawQuery: "filter=",
			want:     nil,
			wantErr:  false,
		},
		{
			name:     "success - missing value",
			rawQuery: "",
			want:     nil,
			wantErr:  false,
		},
		{
			name:     "error - invalid json",
			rawQuery: "filter=%5B",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New(tt.opts...)
			require.NoError(t, err)

			u := &url.URL{
				RawQuery: tt.rawQuery,
			}
			rules, err := p.ParseURLQuery(u.Query())

			if tt.wantErr {
				require.Error(t, err, "ParseURLQuery() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, rules, "ParseURLQuery rules = %v, want %v", rules, tt.want)
			}
		})
	}
}

//nolint:maintidx
func TestFilter_Apply(t *testing.T) {
	t.Parallel()

	type simpleStruct struct {
		StringField    string `json:"string_field"`
		IntField       int    `json:"int_field,omitempty"`
		Float64Field   float64
		StringPtrField *string
		unexported     string
	}

	type complexStruct struct {
		Internal simpleStruct `json:"internal"`
	}

	type complexStructWithPtr struct {
		Internal *simpleStruct
	}

	type embeddingStruct struct {
		simpleStruct
	}

	trueRegex := Rule{
		Field: "",
		Type:  TypeRegexp,
		Value: ".*",
	}
	falseRegex := Rule{
		Field: "",
		Type:  TypeRegexp,
		Value: "$a",
	}

	tests := []struct {
		name             string
		rules            [][]Rule
		opts             []Option
		elements         any
		want             any
		wantTotalMatches uint
		wantErr          bool
	}{
		{
			name: "success - nested string equal",
			elements: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.StringField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - nested string not equal",
			elements: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.StringField",
				Type:  TypePrefixNot + TypeEqual,
				Value: "value 1",
			}}},
			want: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - nested regex",
			elements: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.StringField",
				Type:  TypeRegexp,
				Value: ".* 1",
			}}},
			want: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - int equal",
			elements: &[]simpleStruct{
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeEqual,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					IntField: 42,
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - float64 equal",
			elements: &[]simpleStruct{
				{
					Float64Field: 42,
				},
				{
					Float64Field: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "Float64Field",
				Type:  TypeEqual,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					Float64Field: 42,
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - nil equal",
			elements: &[]simpleStruct{
				{
					StringPtrField: new("value 1"),
				},
				{
					StringPtrField: nil,
				},
			},
			rules: [][]Rule{{{
				Field: "StringPtrField",
				Type:  TypeEqual,
				Value: nil,
			}}},
			want: &[]simpleStruct{
				{
					StringPtrField: nil,
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - int lt",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeLT,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					IntField: 41,
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - int lte",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeLTE,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
			},
			wantTotalMatches: 2,
		},
		{
			name: "success - int gt",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeGT,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					IntField: 43,
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - int gte",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeGTE,
				Value: 42,
			}}},
			want: &[]simpleStruct{
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			wantTotalMatches: 2,
		},
		{
			name: "error - non numeric type for gt",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeGT,
				Value: "error",
			}}},
			wantErr: true,
		},
		{
			name: "error - non numeric type for gte",
			elements: &[]simpleStruct{
				{
					IntField: 41,
				},
				{
					IntField: 42,
				},
				{
					IntField: 43,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeGTE,
				Value: "error",
			}}},
			wantErr: true,
		},
		{
			name: "error - non string type for not-contains",
			elements: &[]simpleStruct{
				{
					StringField: "Alpha",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypePrefixNot + TypeContains,
				Value: 5,
			}}},
			wantErr: true,
		},
		{
			name: "success - equal fold",
			elements: &[]simpleStruct{
				{
					StringField: "Alpha",
				},
				{
					StringField: "Beta",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeEqualFold,
				Value: "beta",
			}}},
			want:             &[]simpleStruct{{StringField: "Beta"}},
			wantTotalMatches: 1,
		},
		{
			name: "success - has prefix",
			elements: &[]simpleStruct{
				{
					StringField: "Alpha",
				},
				{
					StringField: "Beta",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeHasPrefix,
				Value: "Be",
			}}},
			want:             &[]simpleStruct{{StringField: "Beta"}},
			wantTotalMatches: 1,
		},
		{
			name: "success - has suffix",
			elements: &[]simpleStruct{
				{
					StringField: "Alpha",
				},
				{
					StringField: "Beta",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeHasSuffix,
				Value: "ta",
			}}},
			want:             &[]simpleStruct{{StringField: "Beta"}},
			wantTotalMatches: 1,
		},
		{
			name: "success - invalid filter value type",
			elements: &[]simpleStruct{
				{
					StringField: "value 1",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeEqual,
				Value: 42,
			}}},
			want:             &[]simpleStruct{},
			wantTotalMatches: 0,
		},
		{
			name: "success - regexp with an int",
			elements: &[]simpleStruct{
				{
					IntField: 42,
				},
			},
			rules: [][]Rule{{{
				Field: "IntField",
				Type:  TypeRegexp,
				Value: "42",
			}}},
			want:             &[]simpleStruct{},
			wantTotalMatches: 0,
		},
		{
			name: "success - mismatched array",
			elements: &[]any{
				complexStructWithPtr{
					Internal: &simpleStruct{
						StringField: "value 1",
					},
				},
				complexStruct{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.StringField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want: &[]any{
				complexStructWithPtr{
					Internal: &simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - with field tags",
			elements: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					Internal: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			opts: []Option{WithFieldNameTag("json")},
			rules: [][]Rule{{{
				Field: "internal.string_field",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want: &[]complexStruct{
				{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - with field tags with commas",
			elements: &[]complexStruct{
				{
					Internal: simpleStruct{
						IntField: 1,
					},
				},
				{
					Internal: simpleStruct{
						IntField: 2,
					},
				},
			},
			opts: []Option{WithFieldNameTag("json")},
			rules: [][]Rule{{{
				Field: "internal.int_field",
				Type:  TypeEqual,
				Value: 1,
			}}},
			want: &[]complexStruct{
				{
					Internal: simpleStruct{
						IntField: 1,
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - with embedding struct",
			elements: &[]embeddingStruct{
				{
					simpleStruct: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					simpleStruct: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want: &[]embeddingStruct{
				{
					simpleStruct: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name: "success - with embedding struct and field tags",
			elements: &[]embeddingStruct{
				{
					simpleStruct: simpleStruct{
						StringField: "value 1",
					},
				},
				{
					simpleStruct: simpleStruct{
						StringField: "value 2",
					},
				},
			},
			opts: []Option{
				WithFieldNameTag("json"),
			},
			rules: [][]Rule{{{
				Field: "string_field",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want: &[]embeddingStruct{
				{
					simpleStruct: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			wantTotalMatches: 1,
		},
		{
			name:     "success - with root field selector",
			elements: &[]int{41, 42, 43},
			rules: [][]Rule{{{
				Field: "",
				Type:  TypeEqual,
				Value: 42,
			}}},
			want:             &[]int{42},
			wantTotalMatches: 1,
		},
		{
			name:             "success - with empty AND filter",
			elements:         &[]int{41, 42, 43},
			rules:            [][]Rule{},
			want:             &[]int{41, 42, 43},
			wantTotalMatches: 3,
		},
		{
			// An empty OR group is an empty disjunction (always false) that would silently
			// drop every element, so it is rejected rather than treated as "match nothing".
			name:     "error - empty OR group",
			elements: &[]int{41, 42, 43},
			rules:    [][]Rule{{}},
			wantErr:  true,
		},
		{
			name: "success - nested path not found",
			elements: &[]any{
				complexStruct{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.InvalidField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want:             &[]any{},
			wantTotalMatches: 0,
		},
		{
			name: "success - with field tag not found",
			elements: &[]any{
				complexStruct{
					Internal: simpleStruct{
						StringField: "value 1",
					},
				},
			},
			opts: []Option{WithFieldNameTag("json")},
			rules: [][]Rule{{{
				Field: "internal.invalid_field",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want:             &[]any{},
			wantTotalMatches: 0,
		},
		{
			name:             "success - with max results option",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			opts:             []Option{WithMaxResults(3)},
			rules:            [][]Rule{{trueRegex}},
			want:             &[]string{"1", "2", "3"},
			wantTotalMatches: 5,
		},
		{
			name:             "combination - true AND true",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{trueRegex}, {trueRegex}},
			want:             &[]string{"a"},
			wantTotalMatches: 1,
		},
		{
			name:             "combination - true AND false",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{trueRegex}, {falseRegex}},
			want:             &[]string{},
			wantTotalMatches: 0,
		},
		{
			name:             "combination - false AND true",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{falseRegex}, {trueRegex}},
			want:             &[]string{},
			wantTotalMatches: 0,
		},
		{
			name:     "combination - false AND false",
			elements: &[]string{"a"},
			rules:    [][]Rule{{falseRegex}, {falseRegex}},
			want:     &[]string{},
		},
		{
			name:             "combination - true OR false",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{trueRegex, falseRegex}},
			want:             &[]string{"a"},
			wantTotalMatches: 1,
		},
		{
			name:             "combination - true OR true",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{trueRegex, trueRegex}},
			want:             &[]string{"a"},
			wantTotalMatches: 1,
		},
		{
			name:             "combination - false OR true",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{falseRegex, trueRegex}},
			want:             &[]string{"a"},
			wantTotalMatches: 1,
		},
		{
			name:             "combination - false OR false",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{falseRegex, falseRegex}},
			want:             &[]string{},
			wantTotalMatches: 0,
		},
		{
			name:             "combination - (false OR true) AND (true OR false)",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{falseRegex, trueRegex}, {trueRegex, falseRegex}},
			opts:             []Option{WithMaxRules(4)},
			want:             &[]string{"a"},
			wantTotalMatches: 1,
		},
		{
			name:             "combination - (false OR true) AND (false OR false)",
			elements:         &[]string{"a"},
			rules:            [][]Rule{{falseRegex, trueRegex}, {falseRegex, falseRegex}},
			opts:             []Option{WithMaxRules(4)},
			want:             &[]string{},
			wantTotalMatches: 0,
		},
		{
			name:     "error - not a pointer",
			elements: 42,
			rules: [][]Rule{{{
				Type: TypeEqual,
			}}},
			wantErr: true,
		},
		{
			name:     "error - not a slice",
			elements: &simpleStruct{},
			rules: [][]Rule{{{
				Type: TypeEqual,
			}}},
			wantErr: true,
		},
		{
			name: "error - unexported field",
			elements: &[]any{
				complexStruct{
					Internal: simpleStruct{
						unexported: "value 1",
					},
				},
			},
			rules: [][]Rule{{{
				Field: "Internal.unexported",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			// A nil interface element has no fields: the selector is a non-match, so the
			// element is filtered out rather than aborting the whole Apply.
			name: "success - nil item with field selector",
			elements: &[]any{
				nil,
			},
			rules: [][]Rule{{{
				Field: "Somefield",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			want:             &[]any{},
			wantTotalMatches: 0,
		},
		{
			name: "error - nested path inside a basic type",
			elements: &[]any{
				simpleStruct{
					StringField: "value 1",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField.InvalidField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			// A selector absent from a concrete element type can never match: it is a
			// malformed client filter, not a data condition, so it is rejected rather than
			// silently filtering every element out.
			name: "error - unknown field on concrete slice",
			elements: &[]simpleStruct{
				{StringField: "value 1"},
			},
			rules: [][]Rule{{{
				Field: "NoSuchField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			name: "error - unknown nested segment on concrete slice",
			elements: &[]complexStruct{
				{Internal: simpleStruct{StringField: "value 1"}},
			},
			rules: [][]Rule{{{
				Field: "Internal.NoSuchField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			// The JSON-style selector a client would naturally send does not match the Go
			// field names of a Processor configured without WithFieldNameTag.
			name: "error - case-mismatched selector without a field tag",
			elements: &[]complexStruct{
				{Internal: simpleStruct{StringField: "value 1"}},
			},
			rules: [][]Rule{{{
				Field: "internal.string_field",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			name: "error - concrete nested path inside a basic type",
			elements: &[]simpleStruct{
				{StringField: "value 1"},
			},
			rules: [][]Rule{{{
				Field: "StringField.InvalidField",
				Type:  TypeEqual,
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			name: "error - invalid regex",
			elements: &[]simpleStruct{
				{
					StringField: "value 1",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeRegexp,
				Value: "(",
			}}},
			wantErr: true,
		},
		{
			name: "error - not a string and regexp",
			elements: &[]simpleStruct{
				{
					StringField: "value 1",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  TypeRegexp,
				Value: 1,
			}}},
			wantErr: true,
		},
		{
			name: "error - invalid filter type",
			elements: &[]simpleStruct{
				{
					StringField: "value 1",
				},
			},
			rules: [][]Rule{{{
				Field: "StringField",
				Type:  "invalid filter type",
				Value: "value 1",
			}}},
			wantErr: true,
		},
		{
			name:     "error - too many rules",
			elements: &[]int{1, 2, 3},
			rules: [][]Rule{{
				{
					Field: "",
					Type:  TypeEqual,
					Value: 1,
				},
				{
					Field: "",
					Type:  TypeEqual,
					Value: 3,
				},
			}},
			opts:    []Option{WithMaxRules(1)},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New(tt.opts...)
			require.NoError(t, err)

			sliceLen, totalMatches, err := p.Apply(tt.rules, tt.elements)

			if tt.wantErr {
				require.Error(t, err, "Apply() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, tt.elements, "Filtered = %v, want %v", tt.elements, tt.want)
				wantSliceLen := getSliceLen(tt.elements)
				require.Equal(t, wantSliceLen, sliceLen, "Apply() returned sliceLen=%d, want %d", sliceLen, wantSliceLen)
				require.Equal(t, tt.wantTotalMatches, totalMatches, "Apply() returned totalMatches=%d, want %d", totalMatches, tt.wantTotalMatches)
			}
		})
	}
}

func TestFilter_ApplySubset(t *testing.T) {
	t.Parallel()

	trueRegex := Rule{
		Field: "",
		Type:  TypeRegexp,
		Value: ".*",
	}

	tests := []struct {
		name             string
		rules            [][]Rule
		opts             []Option
		elements         any
		offset           uint
		length           uint
		wantTotalMatches uint
		want             any
		wantErr          bool
	}{
		{
			name:             "success - whole slice",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			rules:            [][]Rule{{trueRegex}},
			offset:           0,
			length:           5,
			want:             &[]string{"1", "2", "3", "4", "5"},
			wantTotalMatches: 5,
		},
		{
			name:             "success - contained subset",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			rules:            [][]Rule{{trueRegex}},
			offset:           1,
			length:           3,
			want:             &[]string{"2", "3", "4"},
			wantTotalMatches: 5,
		},
		{
			name:             "success - offset > len(input)",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			rules:            [][]Rule{{trueRegex}},
			offset:           5,
			length:           10,
			want:             &[]string{},
			wantTotalMatches: 5,
		},
		{
			name:             "success - offset in but length out of bounds",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			rules:            [][]Rule{{trueRegex}},
			offset:           3,
			length:           10,
			want:             &[]string{"4", "5"},
			wantTotalMatches: 5,
		},
		{
			name:             "success - no rules with length and offset",
			elements:         &[]string{"1", "2", "3", "4", "5"},
			rules:            [][]Rule{{trueRegex}},
			offset:           2,
			length:           2,
			want:             &[]string{"3", "4"},
			wantTotalMatches: 5,
		},
		{
			name:     "error - length < 1",
			elements: &[]string{"1", "2", "3", "4", "5"},
			rules:    [][]Rule{{trueRegex}},
			offset:   0,
			length:   0,
			wantErr:  true,
		},
		{
			name:     "error - length > p.maxResults",
			elements: &[]string{"1", "2", "3", "4", "5"},
			rules:    [][]Rule{{trueRegex}},
			offset:   0,
			length:   MaxResults + 1,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p, err := New(tt.opts...)
			require.NoError(t, err)

			sliceLen, totalMatches, err := p.ApplySubset(tt.rules, tt.elements, tt.offset, tt.length)

			if tt.wantErr {
				require.Error(t, err, "ApplySubset() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, tt.elements, "Filtered = %v, want %v", tt.elements, tt.want)
				wantSliceLen := getSliceLen(tt.elements)
				require.Equal(t, wantSliceLen, sliceLen, "ApplySubset() returned sliceLen=%d, want %d", sliceLen, wantSliceLen)
				require.Equal(t, tt.wantTotalMatches, totalMatches, "ApplySubset() returned totalMatches=%d, want %d", totalMatches, tt.wantTotalMatches)
			}
		})
	}
}

// TestFilter_Apply_Concurrent ensures a single built [][]Rule can be filtered from many
// goroutines at once without data races (run under -race). Before the fix, Rule.Evaluate
// lazily wrote shared state through a pointer into the rule slice, which raced.
func TestFilter_Apply_Concurrent(t *testing.T) {
	t.Parallel()

	type item struct {
		Name string
		Age  int64
	}

	// A mix of operators so several evaluator kinds are built concurrently.
	rules := [][]Rule{
		{{Field: "Name", Type: TypeRegexp, Value: "^user"}},
		{{Field: "Age", Type: TypeGTE, Value: 18}},
	}

	const goroutines = 32

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()

			p, err := New(WithMaxRules(4))
			assert.NoError(t, err)

			data := []item{
				{Name: "user-a", Age: 20},
				{Name: "guest-b", Age: 40},
				{Name: "user-c", Age: 10},
				{Name: "user-d", Age: 18},
			}

			n, total, aerr := p.Apply(rules, &data)
			assert.NoError(t, aerr)
			assert.Equal(t, uint(2), n)
			assert.Equal(t, uint(2), total)
			assert.Equal(t, []item{{Name: "user-a", Age: 20}, {Name: "user-d", Age: 18}}, data)
		}()
	}

	wg.Wait()
}

// TestFilter_Apply_LargeInt verifies that large int64/uint64 values are filtered using
// exact comparison rather than a lossy float64 widening (which collapses values beyond 2^53).
func TestFilter_Apply_LargeInt(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	t.Run("int64 equality beyond 2^53", func(t *testing.T) {
		t.Parallel()

		target := int64(1)<<53 + 1
		data := []int64{target, target + 1, target - 1}

		n, total, err := p.Apply([][]Rule{{{Type: TypeEqual, Value: target}}}, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []int64{target}, data)
	})

	t.Run("uint64 ordering near MaxUint64", func(t *testing.T) {
		t.Parallel()

		data := []uint64{math.MaxUint64, math.MaxUint64 - 1, math.MaxUint64 - 2}

		n, total, err := p.Apply([][]Rule{{{Type: TypeGT, Value: data[1]}}}, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []uint64{uint64(math.MaxUint64)}, data)
	})
}

// TestFilter_Apply_ErrorLeavesInputUntouched verifies that an error encountered partway
// through filtering does not mutate (truncate or clobber) the caller's slice.
func TestFilter_Apply_ErrorLeavesInputUntouched(t *testing.T) {
	t.Parallel()

	type withField struct {
		Name string
	}

	p, err := New()
	require.NoError(t, err)

	// The first element resolves the "Name" field fine; the second is a plain int,
	// so resolving "Name" on it fails mid-iteration.
	data := []any{
		withField{Name: "keep"},
		42,
		withField{Name: "keep2"},
	}
	original := []any{
		withField{Name: "keep"},
		42,
		withField{Name: "keep2"},
	}

	_, _, err = p.Apply([][]Rule{{{Field: "Name", Type: TypeEqual, Value: "keep"}}}, &data)
	require.Error(t, err)
	require.Equal(t, original, data, "input slice must be left untouched on error")
}

// TestFilter_ParseURLQuery_RuleCap verifies ParseURLQuery rejects rule sets exceeding maxRules.
func TestFilter_ParseURLQuery_RuleCap(t *testing.T) {
	t.Parallel()

	p, err := New(WithMaxRules(1))
	require.NoError(t, err)

	// Two rules in a single AND group, exceeding the max of 1.
	raw := `[[{"field":"a","type":"==","value":1},{"field":"b","type":"==","value":2}]]`

	u := &url.URL{RawQuery: url.Values{"filter": {raw}}.Encode()}

	rules, err := p.ParseURLQuery(u.Query())
	require.Error(t, err)
	require.Nil(t, rules)
}

// TestFilter_Apply_ConcurrentSharedProcessor ensures a single shared Processor can Apply
// from many goroutines at once without data races (run under -race). Before the fix, the
// reflection field cache mutated plain maps with no synchronization, so concurrent Apply
// on one Processor crashed with "concurrent map writes".
func TestFilter_Apply_ConcurrentSharedProcessor(t *testing.T) {
	t.Parallel()

	type address struct {
		Country string
	}

	type person struct {
		Name    string
		Age     int64
		Address *address
	}

	// A mix of field paths so several cache entries are resolved concurrently.
	rules := [][]Rule{
		{{Field: "Name", Type: TypeHasPrefix, Value: "user"}},
		{{Field: "Address.Country", Type: TypeEqual, Value: "EN"}},
		{{Field: "Age", Type: TypeGTE, Value: 18}},
	}

	p, err := New(WithMaxRules(4))
	require.NoError(t, err)

	const goroutines = 32

	var wg sync.WaitGroup

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()

			data := []person{
				{Name: "user-a", Age: 20, Address: &address{Country: "EN"}},
				{Name: "guest-b", Age: 40, Address: &address{Country: "EN"}},
				{Name: "user-c", Age: 10, Address: &address{Country: "EN"}},
				{Name: "user-d", Age: 30, Address: &address{Country: "FR"}},
			}

			n, total, aerr := p.Apply(rules, &data)
			assert.NoError(t, aerr)
			assert.Equal(t, uint(1), n)
			assert.Equal(t, uint(1), total)
			assert.Equal(t, []person{{Name: "user-a", Age: 20, Address: &address{Country: "EN"}}}, data)
		}()
	}

	wg.Wait()
}

// TestFilter_Apply_NilPointerInFieldPath verifies that nil intermediate (and root)
// pointers along a rule field path are treated as a non-match instead of panicking.
func TestFilter_Apply_NilPointerInFieldPath(t *testing.T) {
	t.Parallel()

	type address struct {
		Country string
	}

	type person struct {
		Name    string
		Address *address
	}

	rules := [][]Rule{{{Field: "Address.Country", Type: TypeEqual, Value: "EN"}}}

	t.Run("nil intermediate pointer", func(t *testing.T) {
		t.Parallel()

		p, err := New()
		require.NoError(t, err)

		data := []person{
			{Name: "alpha", Address: nil},
			{Name: "beta", Address: &address{Country: "EN"}},
		}

		n, total, err := p.Apply(rules, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []person{{Name: "beta", Address: &address{Country: "EN"}}}, data)
	})

	t.Run("nil root pointer", func(t *testing.T) {
		t.Parallel()

		p, err := New()
		require.NoError(t, err)

		data := []*person{
			nil,
			{Name: "beta", Address: &address{Country: "EN"}},
		}

		n, total, err := p.Apply(rules, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []*person{{Name: "beta", Address: &address{Country: "EN"}}}, data)
	})
}

// TestParseJSON_RejectsCompositeValue verifies that array and object rule values are rejected
// by the JSON parser (the untrusted-input surface is scalars only), for every operator.
func TestParseJSON_RejectsCompositeValue(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	for _, payload := range []string{
		`[[{"field":"","type":"==","value":{"a":1}}]]`,
		`[[{"field":"","type":"=","value":[1,2]}]]`,
		`[[{"field":"X","type":"<","value":[1]}]]`,
		`[[{"field":"X","type":"regexp","value":{"k":"v"}}]]`,
	} {
		_, perr := p.ParseJSON(payload)
		require.ErrorIs(t, perr, ErrInvalidFilter, "payload %s", payload)
	}
}

// TestFilter_Apply_UncomparableGoValue ensures a Go-constructed rule whose reference value is
// a runtime-uncomparable dynamic type (a struct holding a slice via an any field) cannot panic
// the process: equality falls back to deep comparison instead of the interface "==" operator.
// This value shape is only reachable from a Go caller; the JSON parser rejects it.
func TestFilter_Apply_UncomparableGoValue(t *testing.T) {
	t.Parallel()

	type box struct{ V any }

	for _, typ := range []string{TypeEqual, TypeEqualFold} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			p, err := New()
			require.NoError(t, err)

			// box{V: []int} is a Comparable *type* whose == panics at runtime.
			rules := [][]Rule{{{Field: "", Type: typ, Value: box{V: []int{1, 2}}}}}

			data := []box{
				{V: []int{1, 2}},
				{V: []int{3}},
			}

			n, total, err := p.Apply(rules, &data)
			require.NoError(t, err)
			require.Equal(t, uint(1), n)
			require.Equal(t, uint(1), total)
			require.Equal(t, []box{{V: []int{1, 2}}}, data)
		})
	}
}

// TestFilter_Apply_InterfaceLeafField verifies that a rule targeting an
// interface-typed struct field is evaluated against the concrete value the field
// holds (with a nil interface treated as a non-match).
func TestFilter_Apply_InterfaceLeafField(t *testing.T) {
	t.Parallel()

	type box struct {
		Payload any
	}

	rules := [][]Rule{{{Field: "Payload", Type: TypeEqual, Value: "hit"}}}

	p, err := New()
	require.NoError(t, err)

	data := []box{
		{Payload: "hit"},
		{Payload: "miss"},
		{Payload: nil},
	}

	n, total, err := p.Apply(rules, &data)
	require.NoError(t, err)
	require.Equal(t, uint(1), n)
	require.Equal(t, uint(1), total)
	require.Equal(t, []box{{Payload: "hit"}}, data)
}

// TestFilter_Apply_MaxValueLength rejects rules whose string value (e.g. a regexp
// pattern) exceeds the configured limit, before the evaluator is built.
func TestFilter_Apply_MaxValueLength(t *testing.T) {
	t.Parallel()

	p, err := New(WithMaxValueLength(8))
	require.NoError(t, err)

	data := []string{"123456789"}

	// 9-byte value exceeds the 8-byte limit: rejected before regexp.Compile.
	_, _, err = p.Apply([][]Rule{{{Field: "", Type: TypeRegexp, Value: "123456789"}}}, &data)
	require.Error(t, err)

	// A value within the limit is accepted and evaluated normally.
	n, total, err := p.Apply([][]Rule{{{Field: "", Type: TypeRegexp, Value: "123"}}}, &data)
	require.NoError(t, err)
	require.Equal(t, uint(1), n)
	require.Equal(t, uint(1), total)
}

// TestParseURLQuery_MaxFilterBytes rejects an oversized raw filter payload before it
// is JSON-decoded.
func TestParseURLQuery_MaxFilterBytes(t *testing.T) {
	t.Parallel()

	p, err := New(WithMaxFilterBytes(16))
	require.NoError(t, err)

	q := url.Values{}
	q.Set("filter", `[[{"field":"","type":"==","value":"aaaaaaaaaaaaaaaaaaaa"}]]`)

	_, err = p.ParseURLQuery(q)
	require.Error(t, err)
}

// TestProcessorParseJSON exercises the Processor.ParseJSON limits and success path.
func TestProcessorParseJSON(t *testing.T) {
	t.Parallel()

	p, err := New(WithMaxRules(1), WithMaxFilterBytes(4096))
	require.NoError(t, err)

	// Success.
	rules, err := p.ParseJSON(`[[{"field":"Age","type":"==","value":42}]]`)
	require.NoError(t, err)
	require.Len(t, rules, 1)

	// Payload too large.
	small, err := New(WithMaxFilterBytes(8))
	require.NoError(t, err)
	_, err = small.ParseJSON(`[[{"field":"","type":"==","value":1}]]`)
	require.ErrorIs(t, err, ErrInvalidFilter)

	// Too many rules (small payload, so the byte limit is not what trips).
	_, err = p.ParseJSON(`[[{"field":"","type":"==","value":1},{"field":"","type":"==","value":2}]]`)
	require.ErrorIs(t, err, ErrInvalidFilter)
}

// TestErrInvalidFilter documents and verifies which errors are attributable to the
// client filter (wrapped with ErrInvalidFilter, so a handler can return a generic
// response) versus caller misuse of the Apply arguments (not wrapped).
func TestErrInvalidFilter(t *testing.T) {
	t.Parallel()

	pSmallVal, err := New(WithMaxValueLength(8))
	require.NoError(t, err)

	pFewRules, err := New(WithMaxRules(1))
	require.NoError(t, err)

	type unexportedLeaf struct{ hidden int }

	tests := []struct {
		name        string
		run         func() error
		wantInvalid bool
	}{
		{
			name:        "invalid json",
			run:         func() error { _, e := pFewRules.ParseJSON("not json"); return e },
			wantInvalid: true,
		},
		{
			name: "too many rules",
			run: func() error {
				d := []int{1}
				_, _, e := pFewRules.Apply([][]Rule{{{Type: TypeEqual, Value: 1}, {Type: TypeEqual, Value: 2}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "rule value too large",
			run: func() error {
				d := []string{"x"}
				_, _, e := pSmallVal.Apply([][]Rule{{{Field: "", Type: TypeRegexp, Value: "123456789"}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "unsupported rule type",
			run: func() error {
				d := []string{"x"}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "", Type: "nope", Value: "x"}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "invalid regexp",
			run: func() error {
				d := []string{"x"}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "", Type: TypeRegexp, Value: "("}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "non-string for regexp",
			run: func() error {
				d := []string{"x"}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "", Type: TypeRegexp, Value: 1}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "non-numeric for order",
			run: func() error {
				d := []int{1}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "", Type: TypeLT, Value: "x"}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "field of a basic type",
			run: func() error {
				d := []any{"x"}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "X", Type: TypeEqual, Value: 1}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "unexported field",
			run: func() error {
				d := []unexportedLeaf{{hidden: 1}}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "hidden", Type: TypeEqual, Value: 1}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "field path too deep",
			run: func() error {
				type node struct {
					Value string
					Next  *node
				}

				pShallow, e := New(WithMaxFieldDepth(2))
				if e != nil {
					return e
				}

				d := []node{{Value: "x"}}
				_, _, e = pShallow.Apply([][]Rule{{{Field: "Next.Next.Value", Type: TypeEqual, Value: "x"}}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "too many rule groups",
			run: func() error {
				d := []int{1}
				_, _, e := pFewRules.Apply([][]Rule{{}, {}}, &d)

				return e
			},
			wantInvalid: true,
		},
		{
			name: "caller misuse - not a slice pointer",
			run: func() error {
				d := []int{1}
				_, _, e := pFewRules.Apply([][]Rule{{{Field: "", Type: TypeEqual, Value: 1}}}, d)

				return e
			},
			wantInvalid: false,
		},
		{
			name: "caller misuse - length below 1",
			run: func() error {
				d := []int{1}
				_, _, e := pFewRules.ApplySubset([][]Rule{{{Field: "", Type: TypeEqual, Value: 1}}}, &d, 0, 0)

				return e
			},
			wantInvalid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run()
			require.Error(t, err)
			require.Equal(t, tt.wantInvalid, errors.Is(err, ErrInvalidFilter))
		})
	}
}

// TestFilter_Apply_NamedNumericType verifies that named numeric types (e.g. "type Status int")
// are treated as numbers: they must not match string rules via the rune-string conversion,
// and must match numeric equality and ordering rules.
func TestFilter_Apply_NamedNumericType(t *testing.T) {
	t.Parallel()

	type status int

	type item struct {
		Code status
	}

	newData := func() []item {
		return []item{{Code: 65}, {Code: 40}}
	}

	p, err := New()
	require.NoError(t, err)

	t.Run("does not match string rules", func(t *testing.T) {
		t.Parallel()

		// status(65) must not be coerced to the rune-string "A".
		stringRules := []Rule{
			{Field: "Code", Type: TypeRegexp, Value: "^A$"},
			{Field: "Code", Type: TypeEqual, Value: "A"},
		}

		for _, rule := range stringRules {
			data := newData()

			n, total, err := p.Apply([][]Rule{{rule}}, &data)
			require.NoError(t, err)
			require.Equal(t, uint(0), n)
			require.Equal(t, uint(0), total)
		}
	})

	t.Run("matches numeric ordering", func(t *testing.T) {
		t.Parallel()

		data := newData()

		n, total, err := p.Apply([][]Rule{{{Field: "Code", Type: TypeGT, Value: 50}}}, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []item{{Code: 65}}, data)
	})

	t.Run("matches numeric equality", func(t *testing.T) {
		t.Parallel()

		data := newData()

		n, total, err := p.Apply([][]Rule{{{Field: "Code", Type: TypeEqual, Value: 65}}}, &data)
		require.NoError(t, err)
		require.Equal(t, uint(1), n)
		require.Equal(t, uint(1), total)
		require.Equal(t, []item{{Code: 65}}, data)
	})
}

// TestFilter_Apply_TypeNameCollision ensures the field cache distinguishes identically
// named types (which share the same reflect.Type String()) instead of reusing the
// reflection path resolved for the first type.
func TestFilter_Apply_TypeNameCollision(t *testing.T) {
	t.Parallel()

	rules := [][]Rule{{{Field: "A", Type: TypeEqual, Value: 1}}}

	p, err := New()
	require.NoError(t, err)

	one := makeCollisionSliceOne()

	n, total, err := p.Apply(rules, one)
	require.NoError(t, err)
	require.Equal(t, uint(1), n)
	require.Equal(t, uint(1), total)

	// The second type has the same String() ("filter.collideT") but a different layout:
	// field "A" is at a different index, so a name-keyed cache resolves the wrong field.
	two := makeCollisionSliceTwo()

	n, total, err = p.Apply(rules, two)
	require.NoError(t, err)
	require.Equal(t, uint(1), n)
	require.Equal(t, uint(1), total)
}

// makeCollisionSliceOne returns a slice of a function-local type named collideT
// whose field "A" is at index 0.
func makeCollisionSliceOne() any {
	type collideT struct {
		A int
	}

	return &[]collideT{{A: 1}, {A: 2}}
}

// makeCollisionSliceTwo returns a slice of a different function-local type also named
// collideT (same reflect String()) whose field "A" is at index 1.
func makeCollisionSliceTwo() any {
	type collideT struct {
		B string
		A int
	}

	return &[]collideT{{B: "x", A: 1}, {B: "y", A: 3}}
}

// parityRow carries the same numbers in every numeric field kind an element may have, so that
// one rule can be evaluated against each of them.
type parityRow struct {
	I64 int64
	U64 uint64
	F64 float64
	Int int
}

// TestFilter_Apply_JSONGoRuleParity is the regression guard for the JSON path losing integer
// exactness: a rule parsed from JSON must select exactly the same elements as the equivalent
// rule constructed in Go. Before rule values were decoded with json.Number, every JSON number
// arrived as a float64, so an integer beyond 2^53 also matched its float64 neighbors.
func TestFilter_Apply_JSONGoRuleParity(t *testing.T) {
	t.Parallel()

	values := []struct {
		json string
		val  any
	}{
		{json: `0`, val: int64(0)},
		{json: `-1`, val: int64(-1)},
		{json: `9007199254740992`, val: int64(1) << 53},
		{json: `9007199254740993`, val: int64(1)<<53 + 1},
		{json: `-9007199254740993`, val: -(int64(1)<<53 + 1)},
		{json: `9223372036854775807`, val: int64(math.MaxInt64)},
		{json: `9223372036854775808`, val: uint64(math.MaxInt64) + 1},
		{json: `18446744073709551615`, val: uint64(math.MaxUint64)},
		{json: `1.5`, val: 1.5},
		{json: `-0.0`, val: math.Copysign(0, -1)},
	}

	types := []string{TypeEqual, TypeEqualFold, TypeLT, TypeLTE, TypeGT, TypeGTE}
	fields := []string{"I64", "U64", "F64", "Int"}

	data := []parityRow{
		{I64: 1 << 53, U64: 1 << 53, F64: 1 << 53, Int: 1 << 53},
		{I64: 1<<53 + 1, U64: 1<<53 + 1, F64: 1<<53 + 1, Int: 1<<53 + 1},
		{I64: math.MaxInt64, U64: math.MaxUint64, F64: math.MaxFloat64, Int: math.MaxInt64},
		{I64: -1, U64: 0, F64: math.Copysign(0, -1), Int: -1},
		{I64: 0, U64: 1, F64: 1.5, Int: 2},
	}

	p, err := New()
	require.NoError(t, err)

	for _, value := range values {
		for _, typ := range types {
			for _, field := range fields {
				jsonRules, err := parseJSON(
					`[[{"field":"` + field + `","type":"` + typ + `","value":` + value.json + `}]]`)
				require.NoError(t, err)

				goRules := [][]Rule{{{Field: field, Type: typ, Value: value.val}}}

				fromJSON := slices.Clone(data)
				_, jsonTotal, err := p.Apply(jsonRules, &fromJSON)
				require.NoError(t, err)

				fromGo := slices.Clone(data)
				_, goTotal, err := p.Apply(goRules, &fromGo)
				require.NoError(t, err)

				require.Equal(t, goTotal, jsonTotal, "match count differs for %s %s %s", field, typ, value.json)
				require.Equal(t, fromGo, fromJSON, "matched elements differ for %s %s %s", field, typ, value.json)
			}
		}
	}
}

// TestFilter_Apply_JSONExactInteger pins the concrete failure the parity test guards against:
// 2^53+1 has no exact float64 representation, so a float64-decoded rule value matched 2^53
// too, silently returning a record the client did not ask for.
func TestFilter_Apply_JSONExactInteger(t *testing.T) {
	t.Parallel()

	p, err := New()
	require.NoError(t, err)

	rules, err := p.ParseURLQuery(url.Values{
		DefaultURLQueryFilterKey: {`[[{"field":"I64","type":"==","value":9007199254740993}]]`},
	})
	require.NoError(t, err)

	data := []parityRow{{I64: 1 << 53}, {I64: 1<<53 + 1}}

	n, total, err := p.Apply(rules, &data)
	require.NoError(t, err)
	require.Equal(t, uint(1), n)
	require.Equal(t, uint(1), total)
	require.Equal(t, []parityRow{{I64: 1<<53 + 1}}, data)
}

// TestFilter_Apply_UnknownFieldSelector verifies that a selector absent from a concrete
// element type is reported as a client error, while a selector that can only be resolved per
// element (an interface-typed slice) remains a silent non-match.
func TestFilter_Apply_UnknownFieldSelector(t *testing.T) {
	t.Parallel()

	type row struct {
		Name string
	}

	p, err := New()
	require.NoError(t, err)

	t.Run("concrete element type is a client error", func(t *testing.T) {
		t.Parallel()

		data := []row{{Name: "doe"}}

		_, _, err := p.Apply([][]Rule{{{Field: "NoSuchField", Type: TypeEqual, Value: "doe"}}}, &data)
		require.ErrorIs(t, err, ErrInvalidFilter)

		// Rules are compiled before any filtering, so the input slice is left untouched.
		require.Equal(t, []row{{Name: "doe"}}, data)
	})

	t.Run("interface element type stays a non-match", func(t *testing.T) {
		t.Parallel()

		data := []any{row{Name: "doe"}}

		n, total, err := p.Apply([][]Rule{{{Field: "NoSuchField", Type: TypeEqual, Value: "doe"}}}, &data)
		require.NoError(t, err)
		require.Equal(t, uint(0), n)
		require.Equal(t, uint(0), total)
	})
}

func benchmarkFilterApply(b *testing.B, n int, json string, opts ...Option) {
	b.Helper()

	type simpleStruct struct {
		IntField     int
		Float64Field float64
		SomeField1   any
		SomeField2   any
		SomeField3   any
		StringField  string `json:"string_field"`
	}

	filter, err := New(opts...)
	require.NoError(b, err)

	data := make([]simpleStruct, n)
	for i := range n {
		data[i] = simpleStruct{
			StringField: "hello world",
		}
	}

	rules, err := parseJSON(json)
	require.NoError(b, err)

	b.ResetTimer()

	for range b.N {
		b.StopTimer()

		dataCopy := make([]simpleStruct, len(data))
		copy(dataCopy, data)

		b.StartTimer()

		_, _, err := filter.Apply(rules, &dataCopy)
		require.NoError(b, err)
	}
}

func BenchmarkFilter_Apply_Equal_100(b *testing.B) {
	benchmarkFilterApply(
		b,
		100,
		`[[{"field": "StringField", "type": "==", "value": "hello world"}]]`,
	)
}

func BenchmarkFilter_Apply_Equal_1000(b *testing.B) {
	benchmarkFilterApply(
		b,
		1000,
		`[[{"field": "StringField", "type": "==", "value": "hello world"}]]`,
	)
}

func BenchmarkFilter_Apply_Equal_10000(b *testing.B) {
	benchmarkFilterApply(
		b,
		10000,
		`[[{"field": "StringField", "type": "==", "value": "hello world"}]]`,
	)
}

func BenchmarkFilter_Apply_Regexp_1000(b *testing.B) {
	benchmarkFilterApply(
		b,
		1000,
		`[[{"field": "StringField", "type": "regexp", "value": "hello.*"}]]`,
	)
}

func BenchmarkFilter_Apply_WithTagField_1000(b *testing.B) {
	benchmarkFilterApply(
		b,
		1000,
		`[[{"field": "string_field", "type": "==", "value": "hello world"}]]`,
		WithFieldNameTag("json"),
	)
}
