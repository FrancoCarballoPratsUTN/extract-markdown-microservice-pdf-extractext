package domain

import (
	"encoding/json"
	"net/http"
)

// ProblemDetails es un documento de problema RFC 9457.
type ProblemDetails struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// NewProblem construye un documento de problema con type "about:blank".
func NewProblem(status int, title, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

// WriteProblem escribe p como application/problem+json.
func WriteProblem(w http.ResponseWriter, p ProblemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
