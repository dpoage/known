package sqlite

import (
	"encoding/binary"
	"math"
)

// serializeFloat32 converts a float32 slice to a little-endian byte blob.
func serializeFloat32(v []float32) ([]byte, error) {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf, nil
}

// deserializeFloat32 converts a little-endian BLOB back to a float32 slice.
func deserializeFloat32(data []byte) []float32 {
	if len(data) == 0 {
		return nil
	}
	n := len(data) / 4
	result := make([]float32, n)
	for i := range n {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4 : (i+1)*4]))
	}
	return result
}

// cosineDistance returns the cosine distance (1 - cosine_similarity) between two vectors.
// Returns 2.0 (max distance) for zero-norm vectors.
func cosineDistance(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 2.0
	}
	return 1.0 - dot/denom
}

// l2Distance returns the Euclidean (L2) distance between two vectors.
func l2Distance(a, b []float32) float64 {
	var sum float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return math.Sqrt(sum)
}
