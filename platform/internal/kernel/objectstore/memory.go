package objectstore

import (
	"bytes"
	"context"
	"io"
	"slices"
	"sync"
)

// Memory is an in-process Store for tests.
type Memory struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{objects: map[string][]byte{}} }

var _ Store = (*Memory)(nil)

// Get implements Reader.
func (m *Memory) Get(_ context.Context, key string, maxBytes int64) ([]byte, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	if int64(len(b)) > maxBytes {
		return nil, ErrTooLarge
	}
	return slices.Clone(b), nil
}

// Exists implements Reader.
func (m *Memory) Exists(_ context.Context, key string) (bool, error) {
	if err := CheckKey(key); err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.objects[key]
	return ok, nil
}

// Put implements Writer.
func (m *Memory) Put(_ context.Context, key string, body []byte, _ string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = slices.Clone(body)
	return nil
}

// Delete implements Writer.
func (m *Memory) Delete(_ context.Context, key string) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

var _ StreamStore = (*Memory)(nil)

// PutStream implements Streamer.
func (m *Memory) PutStream(_ context.Context, key string, r io.Reader, _ string) (int64, error) {
	if err := CheckKey(key); err != nil {
		return 0, err
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = b
	return int64(len(b)), nil
}

// Open implements Streamer.
func (m *Memory) Open(_ context.Context, key string) (io.ReadCloser, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(slices.Clone(b))), nil
}

// Keys lists the stored keys, sorted (tests).
func (m *Memory) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
