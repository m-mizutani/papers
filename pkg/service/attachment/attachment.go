// Package attachment implements interfaces.AttachmentStore: Cloud Storage
// in production, and the memory or a local directory in development (D8).
package attachment

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func notFound(key string) error {
	return goerr.Wrap(model.ErrNotFound, "attachment object not found", goerr.V("key", key))
}

// validKey rejects keys that could escape a directory.
func validKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid object key", goerr.V("key", key))
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return goerr.Wrap(model.ErrInvalidArgument, "invalid object key", goerr.V("key", key))
		}
	}
	return nil
}

// Memory keeps objects in memory.
type Memory struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

var _ interfaces.AttachmentStore = (*Memory)(nil)

func NewMemory() *Memory { return &Memory{objects: map[string][]byte{}} }

func (m *Memory) Put(_ context.Context, key, _ string, r io.Reader) error {
	if err := validKey(key); err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return goerr.Wrap(err, "failed to read object")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, notFound(key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func (m *Memory) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			delete(m.objects, k)
		}
	}
	return nil
}

func (m *Memory) SignedURL(context.Context, string, time.Duration) (string, error) { return "", nil }

// Keys lists the stored keys (tests).
func (m *Memory) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.objects))
	for k := range m.objects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Dir keeps objects as files under a directory.
type Dir struct {
	root string
}

var _ interfaces.AttachmentStore = (*Dir)(nil)

func NewDir(root string) (*Dir, error) {
	if root == "" {
		return nil, goerr.New("attachment directory is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, goerr.Wrap(err, "failed to create attachment directory", goerr.V("dir", root))
	}
	return &Dir{root: root}, nil
}

func (d *Dir) path(key string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	return filepath.Join(d.root, filepath.FromSlash(key)), nil
}

func (d *Dir) Put(_ context.Context, key, _ string, r io.Reader) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return goerr.Wrap(err, "failed to create directory")
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return goerr.Wrap(err, "failed to create file")
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return goerr.Wrap(err, "failed to write file")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return goerr.Wrap(err, "failed to close file")
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return goerr.Wrap(err, "failed to move file into place")
	}
	return nil
}

func (d *Dir) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, notFound(key)
	}
	f, err := os.Open(p) // #nosec G304 -- key is validated and joined under root
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notFound(key)
		}
		return nil, goerr.Wrap(err, "failed to open file")
	}
	return f, nil
}

func (d *Dir) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return goerr.Wrap(err, "failed to delete file")
	}
	return nil
}

func (d *Dir) DeletePrefix(_ context.Context, prefix string) error {
	dir := strings.TrimSuffix(prefix, "/")
	p, err := d.path(dir)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p); err != nil {
		return goerr.Wrap(err, "failed to delete directory")
	}
	return nil
}

func (d *Dir) SignedURL(context.Context, string, time.Duration) (string, error) { return "", nil }
