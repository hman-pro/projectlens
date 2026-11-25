package smoketest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
)

const fakeModelName = "qwen3-embedding:0.6b"

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// FakeOllama is an httptest server that counts the /api/embed requests it serves.
type FakeOllama struct {
	*httptest.Server
	embedCalls atomic.Int64
}

// EmbedCalls returns how many POST /api/embed requests have been served.
func (f *FakeOllama) EmbedCalls() int64 { return f.embedCalls.Load() }

// NewFakeOllama starts an httptest server that mimics the Ollama endpoints
// ProjectLens uses: POST /api/embed and GET /api/tags. Embeddings are
// deterministic functions of the input text with the given dimensionality.
func NewFakeOllama(dim int) *FakeOllama {
	f := &FakeOllama{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/embed", func(w http.ResponseWriter, r *http.Request) {
		f.embedCalls.Add(1)
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		resp := embedResponse{Model: req.Model, Embeddings: make([][]float64, len(req.Input))}
		for i, text := range req.Input {
			resp.Embeddings[i] = FakeVector(text, dim)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"` + fakeModelName + `"}]}`))
	})
	f.Server = httptest.NewServer(mux)
	return f
}

// FakeVector returns a deterministic unit-length vector of length dim derived
// from text: a PCG seeded by sha256(text) feeds NormFloat64, then L2-normalized.
func FakeVector(text string, dim int) []float64 {
	sum := sha256.Sum256([]byte(text))
	rng := rand.New(rand.NewPCG(binary.LittleEndian.Uint64(sum[0:8]), binary.LittleEndian.Uint64(sum[8:16])))
	vec := make([]float64, dim)
	var norm float64
	for i := range vec {
		vec[i] = rng.NormFloat64()
		norm += vec[i] * vec[i]
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return vec
	}
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}
