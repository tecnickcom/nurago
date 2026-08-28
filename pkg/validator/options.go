package validator

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"

	vt "github.com/go-playground/validator/v10"
)

// Option is the interface that allows to set configuration options.
type Option func(v *Validator) error

// WithFieldNameTag configures field lookup to use the given struct tag (e.g., "json") instead of field names.
func WithFieldNameTag(tag string) Option {
	return func(v *Validator) error {
		if tag == "" {
			return nil
		}

		v.v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name, _, _ := strings.Cut(fld.Tag.Get(tag), ",")
			if name == "-" {
				return ""
			}

			return name
		})

		return nil
	}
}

// WithCustomValidationTags registers custom domain-specific validation rules (e.g., e164noplus, ein, zipcode).
func WithCustomValidationTags(t map[string]vt.FuncCtx) Option {
	return func(v *Validator) error {
		for tag, fn := range t {
			err := v.v.RegisterValidationCtx(tag, fn)
			if err != nil {
				return fmt.Errorf("failed registering custom tag: %w", err)
			}
		}

		return nil
	}
}

// WithCustomTypeFunc registers a custom type converter for specialized types before validation.
func WithCustomTypeFunc(fn vt.CustomTypeFunc, types ...any) Option {
	return func(v *Validator) error {
		v.v.RegisterCustomTypeFunc(fn, types...)
		return nil
	}
}

// WithErrorTemplates registers text/template-based error message translations for validation tags, taking precedence over upstream messages.
func WithErrorTemplates(t map[string]string) Option {
	return func(v *Validator) error {
		if len(v.tpl) == 0 {
			v.tpl = make(map[string]*template.Template, len(t))
		}

		for tag, tpl := range t {
			parsed, err := template.New(tag).Parse(tpl)
			if err != nil {
				return fmt.Errorf("failed adding error template %q: %w", tag, err)
			}

			// text/template only reports references to non-existent fields at
			// execution time, so exercise the template against a sample Error to
			// fail fast instead of silently degrading to the generic message.
			err = parsed.Execute(io.Discard, &Error{})
			if err != nil {
				return fmt.Errorf("failed validating error template %q: %w", tag, err)
			}

			v.tpl[tag] = parsed
		}

		return nil
	}
}
