package validator

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"

	vt "github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
)

func TestWithFieldNameTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tag  string
	}{
		{
			name: "tag is empty string",
			tag:  "",
		},
		{
			name: "success return name",
			tag:  "abcderfghijk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := &Validator{v: vt.New()}
			err := WithFieldNameTag(tt.tag)(v)

			require.NoError(t, err)
		})
	}
}

func TestWithCustomValidationTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arg     map[string]vt.FuncCtx
		wantErr bool
	}{
		{
			name:    "success with default custom tags",
			arg:     CustomValidationTags(),
			wantErr: false,
		},
		{
			name:    "success with empty tags",
			arg:     map[string]vt.FuncCtx{},
			wantErr: false,
		},
		{
			name:    "error with invalid tag",
			arg:     map[string]vt.FuncCtx{"error": nil},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := &Validator{v: vt.New()}
			err := WithCustomValidationTags(tt.arg)(v)

			if tt.wantErr {
				require.Error(t, err, "error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err, "unexpected error = %v", err)
			}
		})
	}
}

func ValidateValuer(field reflect.Value) any {
	if valuer, ok := reflect.TypeAssert[driver.Valuer](field); ok {
		val, err := valuer.Value()
		if err == nil {
			return val
		}
	}

	return nil
}

func TestWithCustomTypeFunc(t *testing.T) {
	t.Parallel()

	validator, err := New(WithCustomTypeFunc(ValidateValuer, sql.NullString{}, sql.NullInt64{}))
	require.NoError(t, err)

	type DBBackedUser struct {
		Name sql.NullString `validate:"required"`
		Age  sql.NullInt64  `validate:"required"`
	}

	x := DBBackedUser{Name: sql.NullString{String: "", Valid: true}, Age: sql.NullInt64{Int64: 0, Valid: false}}

	err = validator.ValidateStructCtx(t.Context(), x)
	require.Error(t, err)
}

func TestWithErrorTemplates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		arg     map[string]string
		wantErr bool
	}{
		{
			name:    "success with default templates",
			arg:     ErrorTemplates(),
			wantErr: false,
		},
		{
			name:    "success with one templates",
			arg:     map[string]string{"test": "field {{.Tag}}"},
			wantErr: false,
		},
		{
			name:    "success with empty template",
			arg:     map[string]string{},
			wantErr: false,
		},
		{
			name:    "error with invalid template",
			arg:     map[string]string{"test": "{{.Something} missing closing curly brace"},
			wantErr: true,
		},
		{
			name:    "error with template referencing unknown field",
			arg:     map[string]string{"test": "{{.DoesNotExist}}"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := &Validator{v: vt.New()}
			err := WithErrorTemplates(tt.arg)(v)

			if tt.wantErr {
				require.Error(t, err, "error = %v, wantErr %v", err, tt.wantErr)
			} else {
				require.NoError(t, err, "unexpected error = %v", err)
				require.Len(t, v.tpl, len(tt.arg), "Not all templates were imported")
			}
		})
	}
}
