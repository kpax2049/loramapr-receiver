package protocoladapter

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

type SerialLeaseRegistry struct {
	mu     sync.Mutex
	owners map[string]string
}

func NewSerialLeaseRegistry() *SerialLeaseRegistry {
	return &SerialLeaseRegistry{owners: make(map[string]string)}
}

func (r *SerialLeaseRegistry) Acquire(path string, adapter string) (func(), error) {
	path = normalizeDevicePath(path)
	adapter = strings.TrimSpace(adapter)
	if path == "" {
		return nil, fmt.Errorf("serial device path is required")
	}
	if adapter == "" {
		return nil, fmt.Errorf("serial adapter name is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if owner := r.owners[path]; owner != "" {
		return nil, fmt.Errorf("serial device %q is leased by %s", path, owner)
	}
	r.owners[path] = adapter
	released := false
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if released {
			return
		}
		released = true
		if r.owners[path] == adapter {
			delete(r.owners, path)
		}
	}, nil
}

func (r *SerialLeaseRegistry) Unleased(paths []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = normalizeDevicePath(candidate)
		if candidate == "" || r.owners[candidate] != "" {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func (r *SerialLeaseRegistry) Owner(path string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	owner, ok := r.owners[normalizeDevicePath(path)]
	return owner, ok
}

func normalizeDevicePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}
