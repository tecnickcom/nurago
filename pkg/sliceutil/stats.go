package sliceutil

import (
	"errors"
	"math"
	"slices"

	"github.com/tecnickcom/nurago/pkg/typeutil"
)

// ErrEmptySlice is returned by Stats when the input slice contains no elements.
var ErrEmptySlice = errors.New("input slice is empty")

// DescStats contains descriptive statistics items for a data set.
type DescStats[V typeutil.Number] struct {
	// Count is the total number of items in the data set.
	Count int `json:"count"`

	// Entropy is the entropy of the value distribution, expressed in nats
	// (natural logarithm, base e); divide by ln(2) to convert to bits.
	// It is meaningful only for non-negative data with a positive sum, where the
	// values can be interpreted as an unnormalized probability distribution.
	// For data whose sum is not strictly positive (e.g. signed data, all zeros,
	// or a zero/negative total) the entropy is reported as 0 to avoid NaN/Inf;
	// individual non-positive values are skipped for the same reason.
	Entropy float64 `json:"entropy"`

	// ExKurtosis is the sample excess kurtosis (G2) of the data set: the
	// bias-corrected fourth standardized moment with 3.0 subtracted, so that the
	// excess kurtosis of a normal distribution is zero.
	// It is defined only for n >= 4 with a non-zero standard deviation; otherwise
	// it is reported as 0.
	ExKurtosis float64 `json:"exkurtosis"`

	// Max is the maximum value of the data.
	Max V `json:"max"`

	// MaxID is the index (key) of the Max malue in a data set.
	MaxID int `json:"maxid"`

	// Mean or Average is a central tendency of the data.
	Mean float64 `json:"mean"`

	// MeanDev is the Mean Absolute Deviation: the average of the absolute
	// differences between each value and Mean, normalized by n (unlike the
	// sample Variance, which divides by n-1).
	MeanDev float64 `json:"meandev"`

	// Median is the value that divides the data into 2 equal parts.
	// When the data is sorted, the number of terms on the left and right side of median is the same.
	Median float64 `json:"median"`

	// Min is the minimal value of the data.
	Min V `json:"min"`

	// MinID is the index (key) of the Min malue in a data set.
	MinID int `json:"minid"`

	// Mode is the value with the highest frequency in the data set. Ties are
	// broken deterministically by choosing the smallest tied value.
	// When ModeFreq == 1 no value repeats and there is no true mode; Mode is
	// then the smallest value in the data set.
	Mode V `json:"mode"`

	// ModeFreq is the frequency of the Mode value. A value of 1 means no value
	// repeats in the data set (see Mode).
	ModeFreq int `json:"modefreq"`

	// Range is the difference between the highest (Max) and lowest (Min) value,
	// using the element type V. As with Sum, this can overflow for narrow signed
	// integer element types with extreme values.
	Range V `json:"range"`

	// Skewness measures the asymmetry of the distribution about its mean, as the
	// adjusted Fisher-Pearson standardized moment coefficient (sample G1).
	// It is defined only for n >= 3 with a non-zero standard deviation; otherwise
	// it is reported as 0.
	Skewness float64 `json:"skewness"`

	// StdDev is the sample standard deviation: the square root of the sample
	// Variance (which uses the n-1 divisor).
	StdDev float64 `json:"stddev"`

	// Sum is the total of all values, using the element type V. For narrow
	// integer element types or very large data sets it can overflow; use a wide
	// element type (int64/float64) when exact large totals are required.
	Sum V `json:"sum"`

	// Variance is the sample variance: the sum of squared deviations from Mean
	// divided by n-1 (Bessel's correction). It is 0 for a single element, where
	// the sample variance is undefined.
	Variance float64 `json:"variance"`
}

// Stats computes a DescStats summary for a numeric slice including count, sum, min/max, range, mode, mean, median, and shape metrics.
// It returns ErrEmptySlice if s has no elements.
func Stats[S ~[]V, V typeutil.Number](s S) (*DescStats[V], error) {
	n := len(s)
	if n < 1 {
		return nil, ErrEmptySlice
	}

	ds := &DescStats[V]{
		Count:    n,
		Max:      s[0],
		Median:   float64(s[0]),
		Min:      s[0],
		Mode:     s[0],
		ModeFreq: 1,
		Sum:      s[0],
		Mean:     float64(s[0]),
	}

	if n == 1 {
		return ds, nil
	}

	nf := float64(n)

	ord := slices.Clone(s)
	slices.Sort(ord)

	// For all-distinct data (no value repeats) the mode logic never overrides
	// this fallback, so seed it with the smallest value for a deterministic,
	// order-independent result.
	ds.Mode = ord[0]

	statsCenter(ds, s, ord, n, nf)
	statsVariability(ds, ord, nf)
	statsShape(ds, ord, nf)

	return ds, nil
}

// statsCenter computes min, max, mode/frequency, range, mean, and median.
//
//nolint:gocognit
func statsCenter[S ~[]V, V typeutil.Number](ds *DescStats[V], s, ord S, n int, nf float64) {
	freq := 1

	for i := 1; i < n; i++ {
		v := s[i]

		ds.Sum += v

		if v < ds.Min {
			ds.Min = v
			ds.MinID = i
		} else if v > ds.Max {
			ds.Max = v
			ds.MaxID = i
		}

		if ord[i] == ord[i-1] {
			freq++
		} else {
			if freq > ds.ModeFreq {
				ds.Mode = ord[i-1]
				ds.ModeFreq = freq
			}

			freq = 1
		}
	}

	if freq > ds.ModeFreq {
		ds.Mode = ord[n-1]
		ds.ModeFreq = freq
	}

	ds.Range = ds.Max - ds.Min
	ds.Mean = float64(ds.Sum) / nf

	statsMedian(ds, ord, n)
}

// statsMedian computes the median (50th percentile) of the sorted data.
func statsMedian[S ~[]V, V typeutil.Number](ds *DescStats[V], ord S, n int) {
	midpos := n / 2
	ds.Median = float64(ord[midpos])

	if n%2 == 0 {
		ds.Median = (float64(ord[midpos-1]) + ds.Median) / 2
	}
}

// statsVariability computes entropy, mean deviation, variance, and standard deviation (requires statsCenter to be called first).
//
// Entropy treats the data as an unnormalized probability distribution and is
// only well-defined for non-negative data with a strictly positive sum. When
// the sum is not strictly positive the entropy is left at 0 to avoid NaN/Inf;
// individual non-positive values are skipped for the same reason.
func statsVariability[S ~[]V, V typeutil.Number](ds *DescStats[V], ord S, nf float64) {
	sum := float64(ds.Sum)
	entropyOK := sum > 0

	for _, v := range ord {
		vf := float64(v)
		d := vf - ds.Mean
		ds.MeanDev += math.Abs(d)
		ds.Variance += d * d

		if entropyOK && vf > 0 {
			p := vf / sum
			ds.Entropy -= p * math.Log(p)
		}
	}

	ds.MeanDev /= nf
	ds.Variance /= (nf - 1)
	ds.StdDev = math.Sqrt(ds.Variance)
}

// statsShape computes skewness and excess kurtosis (requires statsVariability to be called first).
// Skewness and excess kurtosis are undefined for constant data (zero standard
// deviation); in that case they are left at 0 to avoid NaN/Inf.
func statsShape[S ~[]V, V typeutil.Number](ds *DescStats[V], ord S, nf float64) {
	if nf < 3 || ds.StdDev == 0 {
		return
	}

	for _, v := range ord {
		d := (float64(v) - ds.Mean) / ds.StdDev
		d3 := d * d * d
		ds.Skewness += d3
		ds.ExKurtosis += d3 * d
	}

	ds.Skewness *= (nf / ((nf - 1) * (nf - 2))) // adjusted Fisher-Pearson standardized moment coefficient

	if nf < 4 {
		ds.ExKurtosis = 0
		return
	}

	// Sample excess kurtosis (G2): bias-corrected scale minus the correction term.
	scale := ((nf + 1) / (nf - 1)) * (nf / (nf - 2)) * (1 / (nf - 3))
	bias := 3 * ((nf - 1) / (nf - 2)) * ((nf - 1) / (nf - 3))
	ds.ExKurtosis = ds.ExKurtosis*scale - bias
}
