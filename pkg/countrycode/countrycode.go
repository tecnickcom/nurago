/*
Package countrycode provides access to ISO-3166 country metadata.

It translates country identifiers into structured records and selects countries
by geographic hierarchy, assignment status, or top-level domain.

CountryData includes:
  - ISO-3166 alpha-2, alpha-3, and numeric codes
  - English and French country names
  - region, sub-region, and intermediate region names and codes
  - assignment status and top-level domain (TLD)

# Data sources and initialization

New(nil) builds a Data instance from embedded defaults sourced from ISO-3166,
the CIA World Factbook, United Nations M49, and Wikipedia. New also accepts a
custom []CountryData dataset for private overrides, curated subsets, or pinned
metadata snapshots.

# Typical usage

Create a resolver with New, then retrieve a single country with
CountryByAlpha2Code, CountryByAlpha3Code, or CountryByNumericCode, or fetch
filtered sets using CountriesByRegionCode, CountriesByStatusName, and related
query methods.

# When To Use

  - You validate or expand country codes.
  - The data should be compiled in rather than fetched.
*/
package countrycode

// CountryData contains the country data to be returned.
type CountryData struct {
	StatusCode             uint8  `json:"statusCode"`
	Status                 string `json:"status"`
	Alpha2Code             string `json:"alpha2Code"`
	Alpha3Code             string `json:"alpha3Code"`
	NumericCode            string `json:"numericCode"`
	NameEnglish            string `json:"nameEnglish"`
	NameFrench             string `json:"nameFrench"`
	Region                 string `json:"region"`
	SubRegion              string `json:"subRegion"`
	IntermediateRegion     string `json:"intermediateRegion"`
	RegionCode             string `json:"regionCode"`
	SubRegionCode          string `json:"subRegionCode"`
	IntermediateRegionCode string `json:"intermediateRegionCode"`
	TLD                    string `json:"tld"`
}

// countryByAlpha2ID materializes a full CountryData record from an internal alpha-2 ID.
//
// It is the core decoder used by all public lookup methods. The function maps
// compact internal keys back into the CountryData fields (codes, names, region
// hierarchy, status, and TLD).
func (d *Data) countryByAlpha2ID(a2 uint16) (*CountryData, error) {
	ck, err := d.countryKeyByAlpha2ID(a2)
	if err != nil {
		return nil, err
	}

	el := decodeCountryKey(ck)

	status, err := d.statusByID(int(el.status))
	if err != nil {
		return nil, err
	}

	cd := &CountryData{
		StatusCode: el.status,
		Status:     status.name,
		Alpha2Code: decodeAlpha2(el.alpha2),
	}

	err = d.populateCountryDetails(cd, el)
	if err != nil {
		return nil, err
	}

	return cd, nil
}

// populateCountryDetails fills the names, region hierarchy, codes, and TLD
// fields of cd from the decoded country key el.
//
// Each optional field is populated only when present in the key, so records
// that carry partial metadata (for example a reserved code with no alpha-3, or
// a custom record with a region but no numeric code) keep whatever data they
// do have instead of being silently reduced to status and alpha-2. It returns
// an error only when a decoded region ID is out of range.
func (d *Data) populateCountryDetails(cd *CountryData, el *countryKeyElem) error {
	if el.alpha3 > 0 {
		cd.Alpha3Code = decodeAlpha3(el.alpha3)
	}

	if el.numeric > 0 {
		cd.NumericCode = numericStr(el.numeric)
	}

	// Names are optional: reserved codes have no entry, so a miss is not an error.
	name, err := d.countryNamesByAlpha2ID(el.alpha2)
	if err == nil {
		cd.NameEnglish = name.EN
		cd.NameFrench = name.FR
	}

	region, err := d.regionByID(int(el.region))
	if err != nil {
		return err
	}

	cd.RegionCode = region.code
	cd.Region = region.name

	subregion, err := d.subRegionByID(int(el.subregion))
	if err != nil {
		return err
	}

	cd.SubRegionCode = subregion.code
	cd.SubRegion = subregion.name

	intregion, err := d.intermediateRegionByID(int(el.intregion))
	if err != nil {
		return err
	}

	cd.IntermediateRegionCode = intregion.code
	cd.IntermediateRegion = intregion.name

	if el.tld > 0 {
		cd.TLD = decodeTLD(el.tld)
	}

	return nil
}

// EnumStatus returns all assignment-status names mapped to their numeric code strings.
func (d *Data) EnumStatus() map[string]string {
	m := make(map[string]string, len(d.dStatusByID))

	for _, v := range d.dStatusByID {
		m[v.name] = v.code
	}

	return m
}

// EnumRegion returns all region names mapped to their M49-style region codes.
func (d *Data) EnumRegion() map[string]string {
	m := make(map[string]string, len(d.dRegionByID))

	for _, v := range d.dRegionByID {
		if v.code == "" && v.name == "" {
			continue // skip the region-less sentinel; it is not a valid code.
		}

		m[v.name] = v.code
	}

	return m
}

// EnumSubRegion returns all sub-region names mapped to their codes.
func (d *Data) EnumSubRegion() map[string]string {
	m := make(map[string]string, len(d.dSubRegionByID))

	for _, v := range d.dSubRegionByID {
		if v.code == "" && v.name == "" {
			continue // skip the region-less sentinel; it is not a valid code.
		}

		m[v.name] = v.code
	}

	return m
}

// EnumIntermediateRegion returns all intermediate-region names mapped to their codes.
func (d *Data) EnumIntermediateRegion() map[string]string {
	m := make(map[string]string, len(d.dIntermediateRegionByID))

	for _, v := range d.dIntermediateRegionByID {
		if v.code == "" && v.name == "" {
			continue // skip the region-less sentinel; it is not a valid code.
		}

		m[v.name] = v.code
	}

	return m
}

// CountryByAlpha2Code returns country metadata for an ISO-3166 alpha-2 code.
//
// Example: "IT" resolves to Italy.
func (d *Data) CountryByAlpha2Code(alpha2 string) (*CountryData, error) {
	a2, err := encodeAlpha2(alpha2)
	if err != nil {
		return nil, err
	}

	return d.countryByAlpha2ID(a2)
}

// CountryByAlpha3Code returns country metadata for an ISO-3166 alpha-3 code.
//
// Example: "ITA" resolves to Italy.
func (d *Data) CountryByAlpha3Code(alpha3 string) (*CountryData, error) {
	a3, err := encodeAlpha3(alpha3)
	if err != nil {
		return nil, err
	}

	a2, err := d.alpha2IDByAlpha3ID(a3)
	if err != nil {
		return nil, err
	}

	return d.countryByAlpha2ID(a2)
}

// CountryByNumericCode returns country metadata for an ISO-3166 numeric code.
//
// Example: "380" resolves to Italy.
func (d *Data) CountryByNumericCode(num string) (*CountryData, error) {
	nid, err := encodeNumeric(num)
	if err != nil {
		return nil, err
	}

	a2, err := d.alpha2IDByNumericID(nid)
	if err != nil {
		return nil, err
	}

	return d.countryByAlpha2ID(a2)
}

// countriesByAlpha2IDs expands internal alpha-2 IDs into CountryData records.
//
// It is a shared helper for multi-country query methods.
func (d *Data) countriesByAlpha2IDs(a2s []uint16) ([]*CountryData, error) {
	cds := make([]*CountryData, 0, len(a2s))

	for _, a2 := range a2s {
		cd, err := d.countryByAlpha2ID(a2)
		if err != nil {
			return nil, err
		}

		cds = append(cds, cd)
	}

	return cds, nil
}

// countriesByRegionID returns countries for an internal region ID.
//
// See EnumRegion for the region catalog exposed to callers.
func (d *Data) countriesByRegionID(id uint8) ([]*CountryData, error) {
	a2s, err := d.alpha2IDsByRegionID(id)
	if err != nil {
		return nil, err
	}

	return d.countriesByAlpha2IDs(a2s)
}

// CountriesByRegionCode returns countries belonging to a region code.
//
// Example: "150" returns Europe. See EnumRegion for valid codes.
func (d *Data) CountriesByRegionCode(code string) ([]*CountryData, error) {
	id, err := d.regionIDByCode(code)
	if err != nil {
		return nil, err
	}

	return d.countriesByRegionID(id)
}

// CountriesByRegionName returns countries belonging to a region name.
//
// Example: "Europe". See EnumRegion for valid names.
func (d *Data) CountriesByRegionName(name string) ([]*CountryData, error) {
	id, err := d.regionIDByName(name)
	if err != nil {
		return nil, err
	}

	return d.countriesByRegionID(id)
}

// countriesBySubRegionID returns countries for an internal sub-region ID.
//
// See EnumSubRegion for the sub-region catalog exposed to callers.
func (d *Data) countriesBySubRegionID(id uint8) ([]*CountryData, error) {
	a2s, err := d.alpha2IDsBySubRegionID(id)
	if err != nil {
		return nil, err
	}

	return d.countriesByAlpha2IDs(a2s)
}

// CountriesBySubRegionCode returns countries belonging to a sub-region code.
//
// Example: "039" returns Southern Europe. See EnumSubRegion for valid codes.
func (d *Data) CountriesBySubRegionCode(code string) ([]*CountryData, error) {
	id, err := d.subRegionIDByCode(code)
	if err != nil {
		return nil, err
	}

	return d.countriesBySubRegionID(id)
}

// CountriesBySubRegionName returns countries belonging to a sub-region name.
//
// Example: "Southern Europe". See EnumSubRegion for valid names.
func (d *Data) CountriesBySubRegionName(name string) ([]*CountryData, error) {
	id, err := d.subRegionIDByName(name)
	if err != nil {
		return nil, err
	}

	return d.countriesBySubRegionID(id)
}

// countriesByIntermediateRegionID returns countries for an internal intermediate-region ID.
//
// See EnumIntermediateRegion for the catalog exposed to callers.
func (d *Data) countriesByIntermediateRegionID(id uint8) ([]*CountryData, error) {
	a2s, err := d.alpha2IDsByIntermediateRegionID(id)
	if err != nil {
		return nil, err
	}

	return d.countriesByAlpha2IDs(a2s)
}

// CountriesByIntermediateRegionCode returns countries in an intermediate-region code.
//
// Example: "014" returns Eastern Africa. See EnumIntermediateRegion for valid codes.
func (d *Data) CountriesByIntermediateRegionCode(code string) ([]*CountryData, error) {
	id, err := d.intermediateRegionIDByCode(code)
	if err != nil {
		return nil, err
	}

	return d.countriesByIntermediateRegionID(id)
}

// CountriesByIntermediateRegionName returns countries in an intermediate-region name.
//
// Example: "Eastern Africa". See EnumIntermediateRegion for valid names.
func (d *Data) CountriesByIntermediateRegionName(name string) ([]*CountryData, error) {
	id, err := d.intermediateRegionIDByName(name)
	if err != nil {
		return nil, err
	}

	return d.countriesByIntermediateRegionID(id)
}

// CountriesByStatusID returns countries that match an internal status ID.
//
// See EnumStatus for status names and code values.
func (d *Data) CountriesByStatusID(id uint8) ([]*CountryData, error) {
	a2s, err := d.alpha2IDsByStatusID(id)
	if err != nil {
		return nil, err
	}

	return d.countriesByAlpha2IDs(a2s)
}

// CountriesByStatusName returns countries that match a status name.
//
// Example: "Officially assigned". See EnumStatus for valid names.
func (d *Data) CountriesByStatusName(name string) ([]*CountryData, error) {
	id, err := d.statusIDByName(name)
	if err != nil {
		return nil, err
	}

	return d.CountriesByStatusID(id)
}

// CountriesByTLD returns countries associated with a top-level domain code.
//
// Example: "it" resolves to Italy.
func (d *Data) CountriesByTLD(tld string) ([]*CountryData, error) {
	code, err := encodeTLD(tld)
	if err != nil {
		return nil, err
	}

	a2s, err := d.alpha2IDsByTLD(code)
	if err != nil {
		return nil, err
	}

	return d.countriesByAlpha2IDs(a2s)
}

// countryKey encodes CountryData into the compact internal country key format.
//
// It is used when loading custom datasets so external records can be converted
// into the same binary representation as embedded defaults. The return values
// are the encoded alpha-2 ID and the packed country key.
//
// Only Status and Alpha2Code are required and validated; every other field is
// best-effort. Absent or malformed optional fields encode to zero (an unset
// value) rather than failing the whole record. Region codes are resolved by
// code to match how the region catalogs are keyed in loadData.
func (d *Data) countryKey(data *CountryData) (uint16, uint64, error) {
	status, err := d.statusIDByName(data.Status)
	if err != nil {
		return 0, 0, err
	}

	alpha2, err := encodeAlpha2(data.Alpha2Code)
	if err != nil {
		return 0, 0, err
	}

	alpha3, _ := encodeAlpha3(data.Alpha3Code)
	numeric, _ := encodeNumeric(data.NumericCode)
	region, _ := d.regionIDByCode(data.RegionCode)
	subregion, _ := d.subRegionIDByCode(data.SubRegionCode)
	intregion, _ := d.intermediateRegionIDByCode(data.IntermediateRegionCode)
	tld, _ := encodeTLD(data.TLD)

	ck := &countryKeyElem{
		status:    status,
		alpha2:    alpha2,
		alpha3:    alpha3,
		numeric:   numeric,
		region:    region,
		subregion: subregion,
		intregion: intregion,
		tld:       tld,
	}

	return ck.alpha2, ck.encodeCountryKey(), nil
}
