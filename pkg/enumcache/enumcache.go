/*
Package enumcache provides thread-safe storage and lookup for enumeration name
and ID mappings.

It maps string names to integer IDs and back, and supports encoding and decoding
enum bitmaps when enum IDs represent bit flags.

Typical usage is set-once, read-many: populate entries during startup using Set,
SetAllIDByName, or SetAllNameByID, then perform lookups from application code.

# When To Use

  - Reference data maps small integer IDs to names and both directions are
    queried.
  - The set is loaded at startup and read constantly afterwards.
*/
package enumcache

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/tecnickcom/nurago/pkg/enumbitmap"
)

var (
	// ErrNameNotFound is returned by ID when the requested name is not cached.
	// Match it with errors.Is.
	ErrNameNotFound = errors.New("enumcache: name not found")

	// ErrIDNotFound is returned by Name when the requested id is not cached.
	// Match it with errors.Is.
	ErrIDNotFound = errors.New("enumcache: ID not found")
)

// IDByName maps enum names to numeric IDs.
type IDByName map[string]int

// NameByID maps integers to string names.
type NameByID map[int]string

// EnumCache stores bidirectional enum mappings (name<->ID).
type EnumCache struct {
	mu sync.RWMutex

	id   IDByName
	name NameByID
}

// New creates an empty thread-safe enum cache.
//
// The cache supports bidirectional name/id lookups and bitmask conversions.
func New() *EnumCache {
	return &EnumCache{
		id:   make(IDByName),
		name: make(NameByID),
	}
}

// Set stores a single enum mapping pair.
//
// Existing values for id or name are overwritten. When the id or name was
// previously associated with a different counterpart, the stale reverse mapping
// is removed so both directions stay consistent.
func (ec *EnumCache) Set(id int, name string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	ec.set(id, name)
}

// Delete removes the mapping for id together with its associated name.
//
// It is a no-op when id is not present. Both internal maps are kept consistent.
func (ec *EnumCache) Delete(id int) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	name, ok := ec.name[id]
	if !ok {
		return
	}

	delete(ec.name, id)
	delete(ec.id, name)
}

// SetAllIDByName bulk-loads enum values from name-to-id input.
//
// It is useful when parsing static definitions keyed by symbolic names.
func (ec *EnumCache) SetAllIDByName(enum IDByName) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	for name, id := range enum {
		ec.set(id, name)
	}
}

// SetAllNameByID bulk-loads enum values from id-to-name input.
//
// It is useful when loading rows from storage keyed by numeric IDs.
func (ec *EnumCache) SetAllNameByID(enum NameByID) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	for id, name := range enum {
		ec.set(id, name)
	}
}

// ID returns the numeric ID associated with name.
//
// It returns an error wrapping ErrNameNotFound when name is not present.
func (ec *EnumCache) ID(name string) (int, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	id, ok := ec.id[name]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNameNotFound, name)
	}

	return id, nil
}

// Name returns the symbolic name associated with id.
//
// It returns an error wrapping ErrIDNotFound when id is not present.
func (ec *EnumCache) Name(id int) (string, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	name, ok := ec.name[id]
	if !ok {
		return "", fmt.Errorf("%w: %d", ErrIDNotFound, id)
	}

	return name, nil
}

// SortNames returns all cached names in ascending lexical order.
//
// This is useful for deterministic output and tests.
func (ec *EnumCache) SortNames() []string {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	sorted := make([]string, 0, len(ec.id))
	for name := range ec.id {
		sorted = append(sorted, name)
	}

	sort.Strings(sorted)

	return sorted
}

// SortIDs returns all cached IDs in ascending numeric order.
//
// This is useful for deterministic output and tests.
func (ec *EnumCache) SortIDs() []int {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	sorted := make([]int, 0, len(ec.name))
	for id := range ec.name {
		sorted = append(sorted, id)
	}

	sort.Ints(sorted)

	return sorted
}

// Len returns the number of cached enum pairs.
func (ec *EnumCache) Len() int {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	return len(ec.id)
}

// Has reports whether name is present in the cache.
func (ec *EnumCache) Has(name string) bool {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	_, ok := ec.id[name]

	return ok
}

// HasID reports whether id is present in the cache.
func (ec *EnumCache) HasID(id int) bool {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	_, ok := ec.name[id]

	return ok
}

// DecodeBinaryMap expands a bitmask value into enum names.
//
// The cache must contain bit-value IDs (single-bit powers of two, 1<<0 through
// 1<<31) mapped to names; IDs that are 0 or multi-bit cannot be decoded and are
// silently unreachable. On unknown set bits the returned error wraps
// enumbitmap.ErrUnknownBitValues while known names are still returned.
func (ec *EnumCache) DecodeBinaryMap(v int) ([]string, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	return enumbitmap.BitMapToStrings(ec.name, v) //nolint:wrapcheck
}

// EncodeBinaryMap combines enum names into a bitmask value.
//
// The cache must contain bit-value IDs (single-bit powers of two, 1<<0 through
// 1<<31) mapped to names for the result to round-trip through DecodeBinaryMap. On
// unknown names the returned error wraps enumbitmap.ErrUnknownStringValues while
// known names are still combined into the bitmask.
func (ec *EnumCache) EncodeBinaryMap(s []string) (int, error) {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	return enumbitmap.StringsToBitMap(ec.id, s) //nolint:wrapcheck
}

// set stores a single id<->name pair, keeping both internal maps consistent.
//
// When the id or the name already maps to a different counterpart, the stale
// reverse-map entry is removed so the two maps never desync. The caller must
// hold the write lock.
func (ec *EnumCache) set(id int, name string) {
	if oldName, ok := ec.name[id]; ok && oldName != name {
		delete(ec.id, oldName)
	}

	if oldID, ok := ec.id[name]; ok && oldID != id {
		delete(ec.name, oldID)
	}

	ec.name[id] = name
	ec.id[name] = id
}
