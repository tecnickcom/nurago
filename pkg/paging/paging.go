/*
Package paging computes pagination metadata (current page, total pages,
previous/next page numbers, and SQL OFFSET/LIMIT values) from three inputs:
current page number, page size, and total item count.

# Usage

[New] accepts the three caller-supplied values and returns a fully populated
[Paging] struct in a single call. Out-of-range inputs are clamped automatically
so callers never need to guard against zero page sizes or page numbers beyond
the last page:

	p := paging.New(currentPage, pageSize, totalItems)
	// p.Offset and p.PageSize are ready for use in a SQL LIMIT/OFFSET clause.
	// p.PreviousPage and p.NextPage are safe to embed in a JSON response.

For cases where only SQL values are needed:

	offset, limit := paging.ComputeOffsetAndLimit(currentPage, pageSize)

# Example

For 17 items displayed 5 per page, navigating to page 3:

	p := paging.New(3, 5, 17)
	// p.CurrentPage     == 3
	// p.PageSize        == 5
	// p.TotalItems      == 17
	// p.TotalPages      == 4
	// p.PreviousPage    == 2
	// p.NextPage        == 4
	// p.Offset          == 10  (used as SQL OFFSET)
	// p.HasPreviousPage == true
	// p.HasNextPage     == true

# Edge cases

  - Empty result set: with totalItems == 0, [New] returns TotalPages == 1,
    CurrentPage == 1 and Offset == 0 (never zero pages and never a panic).
    Callers detect an empty set via TotalItems == 0, not via the page fields.
  - Offset overflow: if the offset multiplication overflows uint (only
    reachable through [ComputeOffsetAndLimit] with a currentPage far beyond the
    data, since [New] clamps currentPage to the last page), the offset is
    clamped to [math.MaxUint] as a "beyond range" sentinel that selects no rows,
    rather than wrapping to a wrong offset.
  - JSON numeric precision: fields are plain integers, but JSON numbers are
    float64 in some clients (e.g. JavaScript), which cannot represent values
    above 2^53 exactly. Realistic pagination magnitudes stay well below that;
    the math.MaxUint offset sentinel does not, so treat it as "no rows".

# When To Use

  - A list endpoint returns page metadata alongside results.
  - You need the matching OFFSET and LIMIT for the query.
*/
package paging

import "math"

// Paging contains all pagination metadata computed from current page, page size, and total item count; all fields are JSON-serializable for API responses.
type Paging struct {
	// CurrentPage is the current page number starting from 1.
	// It is encoded as "page" to mirror the conventional ?page= query parameter.
	CurrentPage uint `json:"page"`

	// PageSize is the maximum number of items that can be contained in a page. It is also the LIMIT in SQL queries.
	PageSize uint `json:"page_size"`

	// TotalItems is the total number of items to be paginated.
	TotalItems uint `json:"total_items"`

	// TotalPages is the total number of pages required to contain all the items. It is always >= 1, even for an empty result set.
	TotalPages uint `json:"total_pages"`

	// PreviousPage is the previous page. It is equal to 1 if we are on the first page (CurrentPage == 1).
	PreviousPage uint `json:"previous_page"`

	// NextPage is the next page. It is equal to TotalPages if we are on the last page (CurrentPage == TotalPages).
	NextPage uint `json:"next_page"`

	// Offset is the zero-based number of items before the current page. It is the OFFSET in SQL queries.
	Offset uint `json:"offset"`

	// HasPreviousPage reports whether a page before CurrentPage exists (CurrentPage > 1).
	HasPreviousPage bool `json:"has_previous_page"`

	// HasNextPage reports whether a page after CurrentPage exists (CurrentPage < TotalPages).
	HasNextPage bool `json:"has_next_page"`
}

// New computes all pagination metadata, clamping inputs to safe ranges: pageSize and currentPage default to 1 if less,
// and currentPage is clamped to totalPages. An empty result set (totalItems == 0) yields a single empty page.
func New(currentPage, pageSize, totalItems uint) Paging {
	pageSize = max(pageSize, 1)
	totalPages := computeTotalPages(totalItems, pageSize)
	currentPage = min(max(currentPage, 1), totalPages)

	return Paging{
		CurrentPage:     currentPage,
		PageSize:        pageSize,
		TotalItems:      totalItems,
		TotalPages:      totalPages,
		PreviousPage:    computePreviousPage(currentPage),
		NextPage:        computeNextPage(currentPage, totalPages),
		Offset:          computeOffset(currentPage, pageSize),
		HasPreviousPage: currentPage > 1,
		HasNextPage:     currentPage < totalPages,
	}
}

// ComputeOffsetAndLimit returns the zero-based SQL OFFSET and LIMIT (page size) for the given currentPage and pageSize,
// auto-clamping both to minimum values of 1. Unlike [New], it has no totalItems to bound against, so it does not clamp
// currentPage to a last page: a page far beyond the data yields a correspondingly large offset, clamped to [math.MaxUint]
// on multiplication overflow so a wrapped offset can never select the wrong rows.
func ComputeOffsetAndLimit(currentPage, pageSize uint) (uint, uint) {
	currentPage = max(currentPage, 1)
	pageSize = max(pageSize, 1)

	return computeOffset(currentPage, pageSize), pageSize
}

// computeOffset computes the zero-based offset for the given current page and page size,
// clamping to math.MaxUint when the multiplication overflows,
// so a wrapped offset cannot silently select the wrong rows.
func computeOffset(currentPage, pageSize uint) uint {
	offset := pageSize * (currentPage - 1)

	if currentPage > 1 && offset/pageSize != currentPage-1 {
		return math.MaxUint
	}

	return offset
}

// computeTotalPages computes the total number of pages required to contain all items,
// using an overflow-safe ceiling division.
func computeTotalPages(totalItems, pageSize uint) uint {
	if totalItems <= pageSize {
		return 1
	}

	pages := totalItems / pageSize
	if totalItems%pageSize != 0 {
		pages++
	}

	return pages
}

// computePreviousPage computes the previous page number.
func computePreviousPage(currentPage uint) uint {
	if currentPage <= 1 {
		return 1
	}

	return currentPage - 1
}

// computeNextPage computes the next page number.
func computeNextPage(currentPage, totalPages uint) uint {
	if currentPage >= totalPages {
		return totalPages
	}

	return currentPage + 1
}
