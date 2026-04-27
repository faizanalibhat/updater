// Package capabilities provides the registry of actions the agent knows how
// to perform. Capabilities are matched by name against actions returned in
// heartbeat responses from the admin server.
package capabilities

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Action is the unit of work delivered by the admin server in a heartbeat
// response.
type Action struct {
	// ID uniquely identifies this action so the agent can ack it back to the
	// admin server (and the server can avoid re-dispatching it).
	ID string `json:"id"`

	// Type is the capability name to invoke (e.g. "update_application").
	Type string `json:"type"`

	// Params is a free-form bag of parameters specific to the capability.
	Params map[string]any `json:"params,omitempty"`
}

// Result is what the agent reports back about a single executed action.
type Result struct {
	ActionID    string    `json:"action_id"`
	Capability  string    `json:"capability"`
	Success     bool      `json:"success"`
	Output      string    `json:"output,omitempty"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// Handler executes a capability. Implementations should be safe to call
// from a single goroutine; the registry serializes invocations.
type Handler func(ctx context.Context, params map[string]any) (output string, err error)

// Registry maps capability names to their handlers.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register adds (or replaces) a capability handler.
func (r *Registry) Register(name string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = h
}

// Names returns the sorted list of registered capability names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for k := range r.handlers {
		out = append(out, k)
	}
	return out
}

// Has reports whether a capability is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.handlers[name]
	return ok
}

// Execute runs the action's capability and returns a Result. If the
// capability is unknown, Result.Success is false and Error explains why.
func (r *Registry) Execute(ctx context.Context, a Action) Result {
	res := Result{
		ActionID:   a.ID,
		Capability: a.Type,
		StartedAt:  time.Now().UTC(),
	}

	r.mu.RLock()
	h, ok := r.handlers[a.Type]
	r.mu.RUnlock()

	if !ok {
		res.CompletedAt = time.Now().UTC()
		res.Error = fmt.Sprintf("unknown capability %q", a.Type)
		log.Printf("✗ action %s: %s", a.ID, res.Error)
		return res
	}

	log.Printf("▶ action %s: invoking capability %q", a.ID, a.Type)
	out, err := h(ctx, a.Params)
	res.Output = out
	res.CompletedAt = time.Now().UTC()
	if err != nil {
		res.Error = err.Error()
		log.Printf("✗ action %s (%s) failed: %v", a.ID, a.Type, err)
	} else {
		res.Success = true
		log.Printf("✓ action %s (%s) completed", a.ID, a.Type)
	}
	return res
}
