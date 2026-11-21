package attachment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"golang.org/x/oauth2/google"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
)

// GCS keeps objects in a Cloud Storage bucket through the JSON API. It
// cannot sign URLs, so the server streams attachments itself.
type GCS struct {
	bucket string
	prefix string
	client *http.Client
	// endpoint is the API root (tests point it at a fake server).
	endpoint string
}

var _ interfaces.AttachmentStore = (*GCS)(nil)

const gcsScope = "https://www.googleapis.com/auth/devstorage.read_write"

type GCSOption func(*GCS)

// WithGCSClient replaces the HTTP client and the API root (tests).
func WithGCSClient(client *http.Client, endpoint string) GCSOption {
	return func(g *GCS) { g.client, g.endpoint = client, strings.TrimRight(endpoint, "/") }
}

// NewGCS uses the application default credentials. prefix is prepended to
// every key (it may be empty).
func NewGCS(ctx context.Context, bucket, prefix string, opts ...GCSOption) (*GCS, error) {
	if bucket == "" {
		return nil, goerr.New("attachment bucket is required")
	}
	g := &GCS{bucket: bucket, prefix: strings.Trim(prefix, "/"), endpoint: "https://storage.googleapis.com"}
	for _, opt := range opts {
		opt(g)
	}
	if g.client == nil {
		client, err := google.DefaultClient(ctx, gcsScope)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to create Cloud Storage client")
		}
		g.client = client
	}
	return g, nil
}

func (g *GCS) name(key string) string {
	if g.prefix == "" {
		return key
	}
	return g.prefix + "/" + key
}

func (g *GCS) objectURL(key string) string {
	return fmt.Sprintf("%s/storage/v1/b/%s/o/%s", g.endpoint, url.PathEscape(g.bucket), url.PathEscape(g.name(key)))
}

func (g *GCS) do(req *http.Request, okNotFound bool) (*http.Response, error) {
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, goerr.Wrap(err, "cloud storage request failed", goerr.V("url", req.URL.Redacted()))
	}
	if resp.StatusCode == http.StatusNotFound && okNotFound {
		_ = resp.Body.Close()
		return nil, nil
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, notFound(req.URL.Path)
		}
		return nil, goerr.New("cloud storage returned an error", goerr.V("status", resp.StatusCode), goerr.V("body", string(body)))
	}
	return resp, nil
}

func (g *GCS) Put(ctx context.Context, key, contentType string, r io.Reader) error {
	if err := validKey(key); err != nil {
		return err
	}
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s", g.endpoint, url.PathEscape(g.bucket), url.QueryEscape(g.name(key)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, r)
	if err != nil {
		return goerr.Wrap(err, "failed to build upload request")
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := g.do(req, false)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func (g *GCS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, notFound(key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.objectURL(key)+"?alt=media", nil)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to build download request")
	}
	resp, err := g.do(req, false)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (g *GCS) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.objectURL(key), nil)
	if err != nil {
		return goerr.Wrap(err, "failed to build delete request")
	}
	resp, err := g.do(req, true)
	if err != nil {
		return err
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	return nil
}

func (g *GCS) DeletePrefix(ctx context.Context, prefix string) error {
	token := ""
	for {
		q := url.Values{"prefix": {g.name(prefix)}, "fields": {"items(name),nextPageToken"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		u := fmt.Sprintf("%s/storage/v1/b/%s/o?%s", g.endpoint, url.PathEscape(g.bucket), q.Encode())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return goerr.Wrap(err, "failed to build list request")
		}
		resp, err := g.do(req, false)
		if err != nil {
			return err
		}
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return goerr.Wrap(err, "failed to decode object list")
		}
		for _, it := range page.Items {
			key := it.Name
			if g.prefix != "" {
				key = strings.TrimPrefix(key, g.prefix+"/")
			}
			if err := g.Delete(ctx, key); err != nil {
				return err
			}
		}
		if page.NextPageToken == "" {
			return nil
		}
		token = page.NextPageToken
	}
}

func (g *GCS) SignedURL(context.Context, string, time.Duration) (string, error) { return "", nil }
