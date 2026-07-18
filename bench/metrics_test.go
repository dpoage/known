//go:build bench

package bench

import "testing"

const epsilon = 1e-6

func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < epsilon
}

// --- PrecisionAtK ---------------------------------------------------------

func TestPrecisionAtK_Basic(t *testing.T) {
	// ranked[:2] = {a,b}; a is relevant (grade 2), b is not => 1/2.
	ranked := []string{"a", "b", "c", "d"}
	rel := Judgments{"a": 2, "c": 1}
	got := PrecisionAtK(ranked, rel, 2)
	if want := 0.5; !approxEqual(got, want) {
		t.Errorf("PrecisionAtK = %v, want %v", got, want)
	}
}

func TestPrecisionAtK_KGreaterThanResultCount(t *testing.T) {
	// Only 2 results returned but k=5: denominator stays 5 (1 hit / 5),
	// penalizing the short result list rather than rewarding it.
	ranked := []string{"a", "b"}
	rel := Judgments{"a": 1}
	got := PrecisionAtK(ranked, rel, 5)
	if want := 0.2; !approxEqual(got, want) {
		t.Errorf("PrecisionAtK = %v, want %v", got, want)
	}
}

func TestPrecisionAtK_EmptyResults(t *testing.T) {
	got := PrecisionAtK(nil, Judgments{"a": 1}, 3)
	if got != 0 {
		t.Errorf("PrecisionAtK(empty) = %v, want 0", got)
	}
}

func TestPrecisionAtK_AllIrrelevant(t *testing.T) {
	got := PrecisionAtK([]string{"x", "y"}, Judgments{}, 2)
	if got != 0 {
		t.Errorf("PrecisionAtK(all irrelevant) = %v, want 0", got)
	}
}

func TestPrecisionAtK_ZeroK(t *testing.T) {
	got := PrecisionAtK([]string{"a"}, Judgments{"a": 1}, 0)
	if got != 0 {
		t.Errorf("PrecisionAtK(k=0) = %v, want 0", got)
	}
}

// --- ReciprocalRank / MRR --------------------------------------------------

func TestReciprocalRank_Basic(t *testing.T) {
	ranked := []string{"x", "y", "a", "z"}
	rel := Judgments{"a": 3}
	got := ReciprocalRank(ranked, rel)
	if want := 1.0 / 3.0; !approxEqual(got, want) {
		t.Errorf("ReciprocalRank = %v, want %v", got, want)
	}
}

func TestReciprocalRank_NoneRelevant(t *testing.T) {
	got := ReciprocalRank([]string{"x", "y"}, Judgments{"a": 1})
	if got != 0 {
		t.Errorf("ReciprocalRank(none relevant) = %v, want 0", got)
	}
}

func TestReciprocalRank_Empty(t *testing.T) {
	got := ReciprocalRank(nil, Judgments{"a": 1})
	if got != 0 {
		t.Errorf("ReciprocalRank(empty) = %v, want 0", got)
	}
}

func TestMRR_Mean(t *testing.T) {
	got := MRR([]float64{1.0, 0.5, 0.0})
	if want := 0.5; !approxEqual(got, want) {
		t.Errorf("MRR = %v, want %v", got, want)
	}
}

func TestMRR_Empty(t *testing.T) {
	got := MRR(nil)
	if got != 0 {
		t.Errorf("MRR(empty) = %v, want 0", got)
	}
}

// --- DCG / IDCG / NDCG ------------------------------------------------------

func TestNDCGAtK_GradedTiesAreOrderInvariant(t *testing.T) {
	// b and c share the same grade (2); swapping their positions must not
	// change DCG (a permutation of equal-value items leaves the sum of
	// value*discount unchanged) or NDCG.
	rel := Judgments{"a": 3, "b": 2, "c": 2}
	ranked1 := []string{"a", "b", "c"}
	ranked2 := []string{"a", "c", "b"}

	d1 := DCGAtK(ranked1, rel, 3)
	d2 := DCGAtK(ranked2, rel, 3)
	if !approxEqual(d1, d2) {
		t.Fatalf("DCGAtK differs across a tie swap: %v vs %v", d1, d2)
	}

	n1 := NDCGAtK(ranked1, rel, 3)
	n2 := NDCGAtK(ranked2, rel, 3)
	if !approxEqual(n1, n2) {
		t.Errorf("NDCGAtK differs across a tie swap: %v vs %v", n1, n2)
	}
	// This particular ranking is already ideal (grades non-increasing), so
	// NDCG should be a perfect 1.0.
	if !approxEqual(n1, 1.0) {
		t.Errorf("NDCGAtK for an already-ideal ranking = %v, want 1.0", n1)
	}
}

func TestNDCGAtK_KGreaterThanResultCount(t *testing.T) {
	// One relevant doc (grade 1) at rank 2 of 2 returned results, but
	// k=5: IDCG@5 = gain(1)*discount(0) = 1*1 = 1 (only one relevant
	// judgment exists to build the ideal ranking from).
	// DCG@5 = gain(1)*discount(1) = 1 * (1/log2(3)) = 0.6309297535715...
	ranked := []string{"x", "a"}
	rel := Judgments{"a": 1}
	got := NDCGAtK(ranked, rel, 5)
	if want := 0.630930; !approxEqual(got, want) {
		t.Errorf("NDCGAtK(k>len) = %v, want %v", got, want)
	}
}

func TestNDCGAtK_EmptyResults(t *testing.T) {
	// No results returned at all: DCG=0. IDCG is still computed from the
	// judgments (there's a known-relevant doc we simply failed to
	// retrieve), so NDCG correctly reports 0 rather than being undefined.
	got := NDCGAtK(nil, Judgments{"a": 1}, 5)
	if got != 0 {
		t.Errorf("NDCGAtK(empty ranked) = %v, want 0", got)
	}
}

func TestNDCGAtK_AllIrrelevant(t *testing.T) {
	// No positive judgments at all => IDCG=0 => NDCG defined as 0, not NaN.
	got := NDCGAtK([]string{"x", "y", "z"}, Judgments{}, 3)
	if got != 0 {
		t.Errorf("NDCGAtK(no relevant judgments) = %v, want 0", got)
	}
}

func TestNDCGAtK_ImperfectRanking(t *testing.T) {
	// Worst-case ordering of three graded docs (ascending instead of
	// descending grade) should score well below 1.0.
	rel := Judgments{"a": 3, "b": 1, "c": 2}
	ranked := []string{"b", "c", "a"}
	got := NDCGAtK(ranked, rel, 3)
	if want := 0.680606; !approxEqual(got, want) {
		t.Errorf("NDCGAtK(imperfect) = %v, want %v", got, want)
	}
	if got >= 1.0 {
		t.Errorf("NDCGAtK(imperfect) = %v, want < 1.0", got)
	}
}

func TestDCGAtK_ZeroKIsZero(t *testing.T) {
	got := DCGAtK([]string{"a"}, Judgments{"a": 1}, 0)
	if got != 0 {
		t.Errorf("DCGAtK(k=0) = %v, want 0", got)
	}
}

func TestIsRelevant(t *testing.T) {
	rel := Judgments{"a": 2, "b": 0}
	if !rel.IsRelevant("a") {
		t.Error("IsRelevant(a) = false, want true")
	}
	if rel.IsRelevant("b") {
		t.Error("IsRelevant(b) = true, want false (grade 0)")
	}
	if rel.IsRelevant("missing") {
		t.Error("IsRelevant(missing) = true, want false (absent key)")
	}
}
