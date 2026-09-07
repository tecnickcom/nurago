/*
Package validator wraps https://github.com/go-playground/validator and adds
custom validation rules, a template-based error translation engine, and a
functional-options API.

# How It Works

[New] creates a [Validator] using a variadic list of [Option] values:

 1. An upstream vt.Validate instance is created and held internally.
 2. Options register field-name tag aliases, custom validation functions,
    custom type functions, and error message templates on that instance.
 3. [Validator.ValidateStruct] (or its context-aware twin
    [Validator.ValidateStructCtx]) runs the upstream validator and transforms
    any [vt.ValidationErrors] into an [errors.Join] aggregate of
    typed [Error] values, each carrying the failed tag, parameter, namespace,
    field name, kind, and the translated human-readable message.

Tag groups joined by "|" are iterated individually so each OR-branch failure
produces an independent error.

# Custom Rules

[WithErrorTemplates] maps a validation tag to a text/template string that
receives an [Error] value, giving access to the namespace, field name, tag,
parameter, kind, and actual value. [ErrorTemplates] returns a map covering the
built-in go-playground/validator tags plus the custom tags.
[CustomValidationTags] registers the custom validators:

  - falseif: conditional negation combinator
  - e164noplus: E.164 phone number without the leading '+'
  - zipcode: US ZIP code (12345 or 12345-6789)
  - usstate: two-letter US state code (including DC)
  - usterritory: two-letter US territory code (AS, GU, MP, PR, VI)
  - datetime_rfc3339: strict RFC-3339 datetime
  - datetime_rfc3339_relaxed: RFC-3339 with space instead of 'T'

[WithFieldNameTag] (commonly "json") makes error namespaces use the serialized
field names rather than the Go struct field names. [WithCustomValidationTags]
and [WithCustomTypeFunc] register project-specific rules. Every failure is an
[Error] value.

# Usage

	v, err := validator.New(
	    validator.WithFieldNameTag("json"),
	    validator.WithCustomValidationTags(validator.CustomValidationTags()),
	    validator.WithErrorTemplates(validator.ErrorTemplates()),
	)
	if err != nil {
	    return err
	}

	type Address struct {
	    Phone   string `json:"phone"    validate:"required,e164noplus"`
	    ZIP     string `json:"zip"      validate:"required,zipcode"`
	    State   string `json:"state"    validate:"required,usstate"`
	}

	err = v.ValidateStruct(Address{Phone: "12345", ZIP: "bad", State: "XX"})
	// err is an errors.Join aggregate containing one *Error per failed field,
	// each with a message like:
	//   "phone must be a valid E.164 formatted phone number without the leading '+' symbol"

# When To Use

  - Request payloads are validated from struct tags.
  - Error messages must be readable and templated rather than raw field
    paths.
  - You need validation rules beyond the built-in set.
*/
package validator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"

	vt "github.com/go-playground/validator/v10"
)

// Validator contains the validator object fields.
type Validator struct {
	// V is the validate object.
	v *vt.Validate

	// tpl contains the map of basic translation templates indexed by tag.
	tpl map[string]*template.Template
}

// New constructs a Validator with registered custom tags, field-name aliases, and error templates from the provided options.
func New(opts ...Option) (*Validator, error) {
	v := &Validator{v: vt.New()}

	for _, applyOpt := range opts {
		err := applyOpt(v)
		if err != nil {
			return nil, err
		}
	}

	return v, nil
}

// ValidateStruct validates struct fields against registered rules, returning an errors.Join aggregate of structured Errors if any fail.
func (v *Validator) ValidateStruct(obj any) error {
	return v.ValidateStructCtx(context.Background(), obj)
}

// ValidateStructCtx validates struct fields, threading ctx through to any
// context-aware custom validators, and returns an errors.Join aggregate of
// structured Errors if any fail. The built-in custom validators do not inspect
// ctx, so cancellation only affects user-supplied context-aware rules.
func (v *Validator) ValidateStructCtx(ctx context.Context, obj any) error {
	vErr := v.v.StructCtx(ctx, obj)

	var valErr vt.ValidationErrors
	if !errors.As(vErr, &valErr) {
		// Non-validation errors (e.g. *vt.InvalidValidationError for a nil or
		// non-struct input) must be surfaced instead of silently discarded.
		//nolint:wrapcheck
		return vErr
	}

	var errs []error

	for _, fe := range valErr {
		// separate tags grouped by OR
		for tag := range strings.SplitSeq(fe.Tag(), "|") {
			errs = append(errs, v.tagError(fe, tag))
		}
	}

	// errors.Join drops nil entries (e.g. the "falseif" helper tag) and returns
	// nil when every entry is nil.
	return errors.Join(errs...)
}

// tagError builds the structured Error for a single validation tag.
// It returns nil for the "falseif" helper tag, which only works in combination
// with another tag and must never surface as an error on its own.
func (v *Validator) tagError(fe vt.FieldError, tag string) error {
	tagKey, tagParam, hasParam := strings.Cut(tag, "=")
	if tagKey == "falseif" {
		return nil
	}

	if !hasParam {
		tagParam = fe.Param()
	}

	namespace := fe.Namespace()

	if idx := strings.Index(namespace, "."); idx != -1 {
		namespace = namespace[idx+1:] // remove root struct name
	}

	ve := &Error{
		Tag:             tagKey,
		Param:           tagParam,
		FullTag:         tag,
		Namespace:       namespace,
		StructNamespace: fe.StructNamespace(),
		Field:           fe.Field(),
		StructField:     fe.StructField(),
		Kind:            fe.Kind().String(),
		Value:           fe.Value(),
	}

	ve.Err = v.translate(ve)

	return ve
}

// translate returns the error message associated with the tag.
func (v *Validator) translate(ve *Error) string {
	if t, ok := v.tpl[ve.Tag]; ok {
		var out bytes.Buffer

		err := t.Execute(&out, ve)
		if err == nil {
			return out.String()
		}
	}

	return fmt.Sprintf("%s is invalid because fails the rule: '%s'", ve.Namespace, ve.FullTag)
}
