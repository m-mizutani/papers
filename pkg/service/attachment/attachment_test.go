package attachment_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/service/attachment"
)

func testStore(t *testing.T, s interfaces.AttachmentStore) {
	ctx := context.Background()
	gt.NoError(t, s.Put(ctx, "workspaces/a/papers/p/attachments/x", "image/png", strings.NewReader("png")))
	gt.NoError(t, s.Put(ctx, "workspaces/a/papers/q/attachments/y", "image/png", strings.NewReader("other")))
	r := gt.R1(s.Get(ctx, "workspaces/a/papers/p/attachments/x")).NoError(t)
	data := gt.R1(io.ReadAll(r)).NoError(t)
	gt.NoError(t, r.Close())
	gt.Equal(t, string(data), "png")

	gt.NoError(t, s.DeletePrefix(ctx, "workspaces/a/papers/p/attachments/"))
	_, err := s.Get(ctx, "workspaces/a/papers/p/attachments/x")
	gt.True(t, errors.Is(err, model.ErrNotFound))
	r = gt.R1(s.Get(ctx, "workspaces/a/papers/q/attachments/y")).NoError(t)
	gt.NoError(t, r.Close())

	gt.NoError(t, s.Delete(ctx, "workspaces/a/papers/q/attachments/y"))
	_, err = s.Get(ctx, "workspaces/a/papers/q/attachments/y")
	gt.True(t, errors.Is(err, model.ErrNotFound))

	gt.Error(t, s.Put(ctx, "../escape", "image/png", strings.NewReader("x")))
	gt.Error(t, s.Put(ctx, "a/../../b", "image/png", strings.NewReader("x")))
	url := gt.R1(s.SignedURL(ctx, "k", 0)).NoError(t)
	gt.Equal(t, url, "")
}

func TestMemory(t *testing.T) { testStore(t, attachment.NewMemory()) }

func TestDir(t *testing.T) {
	s := gt.R1(attachment.NewDir(t.TempDir())).NoError(t)
	testStore(t, s)
}

// fakeGCS implements the few JSON API calls GCS uses.
func fakeGCS(t *testing.T) *httptest.Server {
	var mu sync.Mutex
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		const objPrefix = "/storage/v1/b/bkt/o/"
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/storage/v1/b/bkt/o":
			data, _ := io.ReadAll(r.Body)
			objects[r.URL.Query().Get("name")] = data
			_, _ = w.Write([]byte("{}"))
		case r.Method == http.MethodGet && r.URL.Path == "/storage/v1/b/bkt/o":
			var items []map[string]string
			for k := range objects {
				if strings.HasPrefix(k, r.URL.Query().Get("prefix")) {
					items = append(items, map[string]string{"name": k})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		case strings.HasPrefix(r.URL.Path, objPrefix):
			name := strings.TrimPrefix(r.URL.Path, objPrefix)
			data, ok := objects[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodDelete {
				delete(objects, name)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			_, _ = w.Write(data)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGCS(t *testing.T) {
	srv := fakeGCS(t)
	s := gt.R1(attachment.NewGCS(context.Background(), "bkt", "papers", attachment.WithGCSClient(srv.Client(), srv.URL))).NoError(t)
	testStore(t, s)
}
