/*
Package countryphone resolves international phone number prefixes into country
and regional metadata.

The package models country calling code behavior defined by the International
Telecommunication Union (ITU), including the ITU-T E.123 and E.164 numbering
recommendations.

# How it works

countryphone indexes prefix data in a trie and performs longest-prefix lookup,
giving deterministic matches for variable-length numbering plans. The default
dataset is embedded, and callers can provide custom data through New for
provider-specific or updated plans.

# Typical usage

Create a resolver with New, then call NumberInfo for a prefix or full number.
When no prefix matches, NumberInfo returns an error.

# When To Use

  - You classify phone numbers by origin from the prefix alone.
  - Longest-prefix matching is required, because prefixes overlap.
*/
package countryphone

import (
	"errors"
	"fmt"
	"slices"

	"github.com/tecnickcom/nurago/pkg/numtrie"
	"github.com/tecnickcom/nurago/pkg/phonekeypad"
)

// Sentinel errors returned by the package. They can be matched with errors.Is
// so callers can distinguish a missing prefix from an invalid type code.
var (
	// ErrNoMatch is returned by NumberInfo when no stored prefix matches the
	// input number. It signals a missing lookup target (e.g. maps to HTTP 404).
	ErrNoMatch = errors.New("countryphone: no match for prefix")

	// ErrInvalidNumberType is returned by NumberType when the code is outside
	// the range of known number types. It signals bad caller input.
	ErrInvalidNumberType = errors.New("countryphone: invalid number type")

	// ErrInvalidAreaType is returned by AreaType when the code is outside the
	// range of known area types. It signals bad caller input.
	ErrInvalidAreaType = errors.New("countryphone: invalid area type")
)

// enumNumberType maps NumInfo/InPrefixGroup number-type codes to their labels.
//
//nolint:gochecknoglobals // immutable, read-only lookup table for type labels
var enumNumberType = [...]string{
	"",
	"landline",
	"mobile",
	"pager",
	"satellite",
	"special service",
	"virtual",
	"other",
}

// enumAreaType maps GeoInfo/InPrefixGroup area-type codes to their labels.
//
//nolint:gochecknoglobals // immutable, read-only lookup table for type labels
var enumAreaType = [...]string{
	"",
	"state",
	"province or territory",
	"nation or territory",
	"non-geographic",
	"other",
}

// InPrefixGroup stores the type and geographical information of a group of phone
// number prefixes.
type InPrefixGroup struct {
	// Name is the name of the group or geographical area.
	Name string `json:"name"`

	// Type is the type of group or geographical area:
	//   - 0 = ""
	//   - 1 = "state"
	//   - 2 = "province or territory"
	//   - 3 = "nation or territory"
	//   - 4 = "non-geographic"
	//   - 5 = "other"
	Type int `json:"type"`

	// PrefixType is the type of phone number prefix:
	//   - 0 = ""
	//   - 1 = "landline"
	//   - 2 = "mobile"
	//   - 3 = "pager"
	//   - 4 = "satellite"
	//   - 5 = "special service"
	//   - 6 = "virtual"
	//   - 7 = "other"
	PrefixType int `json:"prefixType"`

	// Prefixes is a list of phone number prefixes (including the Country Code).
	Prefixes []string `json:"prefixes"`
}

// InCountryData stores all the phone number prefixes information for a country.
type InCountryData struct {
	// CC is the Country Calling code (e.g. "1" for "US" and "CA").
	CC string `json:"cc"`

	// Groups is a list of phone prefixes information grouped by geographical
	// area or type.
	Groups []InPrefixGroup `json:"groups"`
}

// InData maps country alpha-2 codes to their dialing metadata.
type InData = map[string]*InCountryData

// GeoInfo stores geographical information of a phone number.
type GeoInfo struct {
	// Alpha2 is the ISO-3166 Alpha-2 Country Code.
	Alpha2 string `json:"alpha2"`

	// Area is the geographical area.
	Area string `json:"area"`

	// Type is the type of area:
	//   - 0 = ""
	//   - 1 = "state"
	//   - 2 = "province or territory"
	//   - 3 = "nation or territory"
	//   - 4 = "non-geographic"
	//   - 5 = "other"
	Type int `json:"type"`
}

// NumInfo stores the number type and geographical information of a phone number.
type NumInfo struct {
	// Type is the type of number:
	//   - 0 = ""
	//   - 1 = "landline"
	//   - 2 = "mobile"
	//   - 3 = "pager"
	//   - 4 = "satellite"
	//   - 5 = "special service"
	//   - 6 = "virtual"
	//   - 7 = "other"
	Type int `json:"type"`

	// Geo is the geographical information.
	Geo []GeoInfo `json:"geo"`
}

// Data stores numbering metadata and prefix indexes used for longest-prefix lookups.
//
// A constructed *Data is effectively read-only: once New returns it is safe for
// concurrent use by multiple goroutines through NumberInfo, NumberType, and
// AreaType. New itself mutates internal state and must not run concurrently with
// operations on the same Data.
type Data struct {
	trie *numtrie.Node[NumInfo]
}

// New builds a prefix resolver backed by a longest-prefix trie.
//
// If data is nil, the embedded default numbering dataset is loaded.
// Precomputing trie indexes at construction keeps NumberInfo lookups
// deterministic for mixed-length international prefixes.
func New(data InData) *Data {
	d := &Data{}

	if data == nil {
		data = defaultData()
	}

	d.loadData(data)

	return d
}

// NumberInfo resolves a phone number or prefix into typed geographic metadata.
//
// The lookup uses longest-prefix matching, so callers can pass either full
// numbers or partial prefixes and still obtain the most specific available
// mapping.
//
// NOTE: see the "github.com/tecnickcom/nurago/pkg/countrycode" package to get the
// country information from the Alpha2 code.
func (d *Data) NumberInfo(num string) (*NumInfo, error) {
	data, status := d.trie.Get(num)

	if status < 0 || data == nil {
		return nil, fmt.Errorf("%w %q", ErrNoMatch, num)
	}

	// Return an independent copy so callers cannot mutate the cached trie state.
	return cloneNumInfo(data), nil
}

// cloneNumInfo returns an independent copy of a NumInfo.
//
// GeoInfo has no reference-type fields, so cloning the slice fully isolates the
// cached trie state from callers: a nil Geo stays nil (slices.Clone(nil) == nil).
func cloneNumInfo(src *NumInfo) *NumInfo {
	return &NumInfo{
		Type: src.Type,
		Geo:  slices.Clone(src.Geo),
	}
}

// NumberType returns the label for a numeric prefix-type code.
//
// It converts integer values into readable type names.
func (d *Data) NumberType(t int) (string, error) {
	if t < 0 || t >= len(enumNumberType) {
		return "", fmt.Errorf("%w %d", ErrInvalidNumberType, t)
	}

	return enumNumberType[t], nil
}

// AreaType returns the label for a numeric area-type code.
//
// It translates stored integer codes into stable human-readable category names.
func (d *Data) AreaType(t int) (string, error) {
	if t < 0 || t >= len(enumAreaType) {
		return "", fmt.Errorf("%w %d", ErrInvalidAreaType, t)
	}

	return enumAreaType[t], nil
}

// insertPrefix inserts or merges prefix metadata into the trie.
//
// When a value is already stored at the exact prefix node, geographic entries
// are merged so multiple countries/areas can be represented for shared
// numbering spaces. Values stored at ancestor prefixes are never merged in, so
// more specific prefixes keep only their own geographic data.
//
// Prefixes without any dialable digit are skipped: they would normalize to the
// empty trie key and install a value on the trie root, turning it into a
// universal fallback that defeats the documented no-match error of NumberInfo.
func (d *Data) insertPrefix(prefix string, info *NumInfo) {
	if !hasDialableDigit(prefix) {
		return
	}

	v := d.trie.GetExact(prefix)

	if (v != nil) && (v != info) && (len(v.Geo) > 0) {
		// the node already exists > merge the data
		if len(info.Geo) > 0 {
			info.Geo = mergeGeo(v.Geo, info.Geo)
		}
	}

	d.trie.Add(prefix, info)
}

// hasDialableDigit reports whether prefix contains at least one character that
// numtrie maps to a keypad digit. Prefixes made only of separators (or empty)
// normalize to the empty trie key and must never be inserted.
func hasDialableDigit(prefix string) bool {
	for _, r := range prefix {
		if _, ok := phonekeypad.KeypadDigit(r); ok {
			return true
		}
	}

	return false
}

// mergeGeo concatenates two GeoInfo slices while skipping duplicate entries.
//
// Deduplication is value-based so repeated or aliased prefixes do not produce
// duplicated GeoInfo, while genuinely distinct areas (e.g. countries sharing a
// calling code) are preserved.
func mergeGeo(existing, added []GeoInfo) []GeoInfo {
	merged := make([]GeoInfo, 0, len(existing)+len(added))
	seen := make(map[GeoInfo]struct{}, len(existing)+len(added))

	appendUnique := func(geo []GeoInfo) {
		for _, g := range geo {
			if _, ok := seen[g]; ok {
				continue
			}

			seen[g] = struct{}{}

			merged = append(merged, g)
		}
	}

	appendUnique(existing)
	appendUnique(added)

	return merged
}

// insertGroups expands country group definitions into trie prefixes.
//
// Each group becomes one or more prefix entries that carry number type and
// geographic annotations. Groups without prefixes fall back to the country
// calling code, unless it is empty (which would install a universal fallback
// on the trie root).
func (d *Data) insertGroups(a2 string, cdata *InCountryData) {
	for _, g := range cdata.Groups {
		if len(g.Prefixes) == 0 {
			// A group without explicit prefixes falls back to the country
			// calling code; insertPrefix skips it when the CC is empty.
			d.insertPrefix(cdata.CC, newGroupInfo(a2, g))

			continue
		}

		// Build an independent NumInfo per prefix so that distinct prefixes
		// never share the same trie value.
		for _, p := range g.Prefixes {
			d.insertPrefix(p, newGroupInfo(a2, g))
		}
	}
}

// newGroupInfo builds a fresh NumInfo (with its own GeoInfo) for a group.
//
// Each call allocates an independent NumInfo so distinct prefixes never share
// the same trie value.
func newGroupInfo(a2 string, g InPrefixGroup) *NumInfo {
	return &NumInfo{
		Type: g.PrefixType,
		Geo: []GeoInfo{
			{
				Alpha2: a2,
				Area:   g.Name,
				Type:   g.Type,
			},
		},
	}
}

// loadData constructs trie nodes from the input numbering dataset.
//
// It inserts a root country-calling-code entry per country and then appends
// optional group-level prefixes for more specific matches. Countries that share
// a calling code accumulate their root geo entries on the same node via
// insertPrefix's merge. Nil entries and entries with an empty country calling
// code (e.g. the default "__" non-geographic entry) contribute only their group
// prefixes; insertPrefix skips any prefix that would install a universal
// fallback on the trie root.
func (d *Data) loadData(data InData) {
	d.trie = numtrie.New[NumInfo]()

	for a2, cdata := range data {
		if cdata == nil {
			continue
		}

		if cdata.CC != "" {
			// Insert the root node for the country calling code.
			d.insertPrefix(cdata.CC, &NumInfo{
				Type: 0,
				Geo: []GeoInfo{
					{
						Alpha2: a2,
						Area:   "",
						Type:   0,
					},
				},
			})
		}

		if len(cdata.Groups) > 0 {
			d.insertGroups(a2, cdata)
		}
	}
}
