package domain

import (
	"encoding/json"
	"net/http"
)

type ProblemDetails struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func NewProblem(status int, title, detail string) ProblemDetails {
	return ProblemDetails{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
	}
}

func WriteProblem(w http.ResponseWriter, p ProblemDetails) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
