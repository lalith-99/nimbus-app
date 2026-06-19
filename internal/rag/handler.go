package rag

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"
)

// Handler exposes the RAG pipeline as an HTTP endpoint.
type Handler struct {
	pipeline *Pipeline
	logger   *zap.Logger
}

// NewHandler creates a new RAG HTTP handler.
func NewHandler(pipeline *Pipeline, logger *zap.Logger) *Handler {
	return &Handler{pipeline: pipeline, logger: logger}
}

// HandleAsk handles POST /v1/ai/ask
//
// Request:
//
//	{"query": "Why did my last 3 emails fail?", "tenant_id": "uuid"}
//
// Response:
//
//	{
//	    "answer": "Your last 3 emails failed because... [1][2]",
//	    "citations": [
//	        {"id": "uuid", "content": "Notification abc: ...", "source_type": "notification"},
//	        ...
//	    ]
//	}
//
// The citations array lets the caller cross-reference the answer against
// real notification records — no hallucinations can hide behind a citation
// that the caller can't verify.
func (h *Handler) HandleAsk(w http.ResponseWriter, r *http.Request) {
	var req AskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrJSON(w, http.StatusBadRequest, "Malformed JSON body", err.Error())
		return
	}

	if req.Query == "" {
		writeErrJSON(w, http.StatusBadRequest, "Missing required field", "query is required")
		return
	}
	if req.TenantID == "" {
		writeErrJSON(w, http.StatusBadRequest, "Missing required field", "tenant_id is required")
		return
	}

	h.logger.Info("RAG ask request",
		zap.String("tenant_id", req.TenantID),
		zap.Int("query_len", len(req.Query)),
	)

	resp, err := h.pipeline.Ask(r.Context(), req)
	if err != nil {
		h.logger.Error("RAG pipeline error",
			zap.Error(err),
			zap.String("tenant_id", req.TenantID),
		)
		// Surface injection blocks as 400, everything else as 500.
		if isBlockedErr(err) {
			writeErrJSON(w, http.StatusBadRequest, "Request blocked", err.Error())
			return
		}
		writeErrJSON(w, http.StatusInternalServerError, "RAG pipeline failed", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeErrJSON(w http.ResponseWriter, statusCode int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": statusCode,
		"title":  title,
		"detail": detail,
	})
}

func isBlockedErr(err error) bool {
	if err == nil {
		return false
	}
	return len(err.Error()) > 16 && err.Error()[:16] == "request blocked:"
}
