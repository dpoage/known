//go:build bench

package bench

import (
	"math"
	"sort"
)

// Judgments maps a ranked result's ID to its graded relevance for one
// query. Grades follow the standard IR convention: 0 (or an absent key) =
// not relevant, positive integers = increasingly relevant (e.g. 1 =
// marginal, 2 = relevant, 3 = highly relevant). PrecisionAtK and
// ReciprocalRank treat any grade > 0 as binary-relevant; NDCGAtK uses the
// full graded value.
type Judgments map[string]int

// IsRelevant reports whether id has a positive relevance grade.
func (j Judgments) IsRelevant(id string) bool {
	return j[id] > 0
}

// PrecisionAtK returns the fraction of the first k ranked results that are
// relevant (grade > 0).
//
// The denominator is always k, not min(k, len(ranked)) -- following the
// standard trec_eval convention -- so a system that returns fewer than k
// results is penalized for the shortfall rather than rewarded for stopping
// early. Returns 0 if k <= 0.
func PrecisionAtK(ranked []string, rel Judgments, k int) float64 {
	if k <= 0 {
		return 0
	}
	n := k
	if n > len(ranked) {
		n = len(ranked)
	}
	hits := 0
	for _, id := range ranked[:n] {
		if rel.IsRelevant(id) {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

// ReciprocalRank returns 1/rank of the first relevant (grade > 0) result in
// ranked, or 0 if ranked contains no relevant result (including when
// ranked is empty).
func ReciprocalRank(ranked []string, rel Judgments) float64 {
	for i, id := range ranked {
		if rel.IsRelevant(id) {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// MRR returns the mean of a set of per-query reciprocal ranks (typically
// produced by ReciprocalRank, one per query). Returns 0 for an empty input.
func MRR(reciprocalRanks []float64) float64 {
	if len(reciprocalRanks) == 0 {
		return 0
	}
	var sum float64
	for _, rr := range reciprocalRanks {
		sum += rr
	}
	return sum / float64(len(reciprocalRanks))
}

// gain converts a graded relevance value into a DCG gain using the standard
// exponential-gain formula (2^grade - 1). Non-positive grades contribute no
// gain.
func gain(grade int) float64 {
	if grade <= 0 {
		return 0
	}
	return math.Exp2(float64(grade)) - 1
}

// discount returns the logarithmic rank discount for a zero-based position,
// i.e. 1/log2(rank+1) with rank = position+1.
func discount(pos int) float64 {
	return 1.0 / math.Log2(float64(pos+2))
}

// DCGAtK returns the discounted cumulative gain of the first k ranked
// results:
//
//	DCG@k = sum_{i=1}^{k} (2^grade_i - 1) / log2(i+1)
//
// Positions beyond len(ranked) contribute 0, matching PrecisionAtK's
// "penalize short lists rather than truncate the denominator" convention.
func DCGAtK(ranked []string, rel Judgments, k int) float64 {
	n := k
	if n > len(ranked) {
		n = len(ranked)
	}
	var dcg float64
	for i := range n {
		dcg += gain(rel[ranked[i]]) * discount(i)
	}
	return dcg
}

// IDCGAtK returns the ideal DCG@k: the DCG obtained by sorting every
// positive-graded judgment in rel in descending order and taking the top k.
// This is the normalizer used by NDCGAtK.
func IDCGAtK(rel Judgments, k int) float64 {
	grades := make([]int, 0, len(rel))
	for _, g := range rel {
		if g > 0 {
			grades = append(grades, g)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	n := k
	if n > len(grades) {
		n = len(grades)
	}
	var idcg float64
	for i := range n {
		idcg += gain(grades[i]) * discount(i)
	}
	return idcg
}

// NDCGAtK returns the normalized DCG@k: DCGAtK(ranked, rel, k) /
// IDCGAtK(rel, k). Returns 0 (never NaN) when IDCGAtK is 0, i.e. when rel
// has no positively-graded judgments at all -- such a query carries no
// relevance signal either way, and callers averaging NDCG across many
// queries should never have to special-case a NaN.
func NDCGAtK(ranked []string, rel Judgments, k int) float64 {
	ideal := IDCGAtK(rel, k)
	if ideal == 0 {
		return 0
	}
	return DCGAtK(ranked, rel, k) / ideal
}
