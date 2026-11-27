package google_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/service/google"
)

type fakeGoogle struct {
	t      *testing.T
	server *httptest.Server
	claims map[string]any
	code   string

	mu         sync.Mutex
	key        jwk.Key
	jwksServed int
}

func newSigningKey(t *testing.T, kid string) jwk.Key {
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	gt.NoError(t, err)
	key := gt.R1(jwk.Import[jwk.Key](raw)).NoError(t)
	gt.NoError(t, key.Set(jwk.KeyIDKey, kid))
	gt.NoError(t, key.Set(jwk.AlgorithmKey, jwa.RS256()))
	return key
}

// rotateKey replaces the signing key, as Google does from time to time.
func (f *fakeGoogle) rotateKey(kid string) {
	key := newSigningKey(f.t, kid)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.key = key
}

func (f *fakeGoogle) signingKey() jwk.Key {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.key
}

func (f *fakeGoogle) jwksRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jwksServed
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{t: t, key: newSigningKey(t, "k1"), code: "good"}

	mux := http.NewServeMux()
	mux.HandleFunc("/certs", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.jwksServed++
		f.mu.Unlock()
		pub := gt.R1(jwk.PublicKeyOf(f.signingKey())).NoError(t)
		set := jwk.NewSet()
		gt.NoError(t, set.AddKey(pub))
		gt.NoError(t, json.NewEncoder(w).Encode(set))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		gt.NoError(t, r.ParseForm())
		if r.Form.Get("code") != f.code || r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		tok := jwt.New()
		for k, v := range f.claims {
			gt.NoError(t, tok.Set(k, v))
		}
		signed := gt.R1(jwt.Sign(tok, jwt.WithKey(jwa.RS256(), f.signingKey()))).NoError(t)
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": string(signed)})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGoogle) validClaims() map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "aud": "client", "sub": "123",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"nonce": "n1", "email": "alice@example.com", "email_verified": true,
		"name": "Alice", "picture": "https://example.com/a.png", "hd": "example.com",
	}
}

func (f *fakeGoogle) provider(t *testing.T) *google.Provider {
	return gt.R1(google.New(google.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://papers.example/api/auth/callback", HostedDomain: "example.com"},
		google.WithEndpoints(f.server.URL+"/auth", f.server.URL+"/token", f.server.URL+"/certs"))).NoError(t)
}

func TestAuthURL(t *testing.T) {
	f := newFakeGoogle(t)
	u := gt.R1(url.Parse(f.provider(t).AuthURL("st", "nn"))).NoError(t)
	q := u.Query()
	gt.Equal(t, q.Get("client_id"), "client")
	gt.Equal(t, q.Get("state"), "st")
	gt.Equal(t, q.Get("nonce"), "nn")
	gt.Equal(t, q.Get("hd"), "example.com")
	gt.True(t, strings.Contains(q.Get("scope"), "openid"))
}

func TestExchange(t *testing.T) {
	ctx := context.Background()

	t.Run("valid token", func(t *testing.T) {
		f := newFakeGoogle(t)
		f.claims = f.validClaims()
		c := gt.R1(f.provider(t).Exchange(ctx, "good", "n1")).NoError(t)
		gt.Equal(t, c.Subject, "123")
		gt.Equal(t, c.Email, "alice@example.com")
		gt.True(t, c.EmailVerified)
		gt.Equal(t, c.Name, "Alice")
		gt.Equal(t, c.Picture, "https://example.com/a.png")
		gt.Equal(t, c.HostedDomain, "example.com")
	})

	cases := map[string]func(m map[string]any){
		"nonce mismatch": func(m map[string]any) { m["nonce"] = "other" },
		"wrong audience": func(m map[string]any) { m["aud"] = "someone-else" },
		"wrong issuer":   func(m map[string]any) { m["iss"] = "https://evil.example" },
		"expired":        func(m map[string]any) { m["exp"] = time.Now().Add(-time.Hour).Unix() },
		"no subject":     func(m map[string]any) { delete(m, "sub") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			f.claims = f.validClaims()
			mutate(f.claims)
			_, err := f.provider(t).Exchange(ctx, "good", "n1")
			gt.Error(t, err)
		})
	}

	t.Run("rotated key is fetched again", func(t *testing.T) {
		f := newFakeGoogle(t)
		f.claims = f.validClaims()
		p := f.provider(t)
		gt.R1(p.Exchange(ctx, "good", "n1")).NoError(t)
		gt.Equal(t, f.jwksRequests(), 1)

		// The cached keys no longer verify the token, so the provider
		// fetches the key set again once.
		f.rotateKey("k2")
		c := gt.R1(p.Exchange(ctx, "good", "n1")).NoError(t)
		gt.Equal(t, c.Subject, "123")
		gt.Equal(t, f.jwksRequests(), 2)
	})

	t.Run("rejected code", func(t *testing.T) {
		f := newFakeGoogle(t)
		f.claims = f.validClaims()
		_, err := f.provider(t).Exchange(ctx, "bad", "n1")
		gt.Error(t, err)
	})

	t.Run("signed by unknown key", func(t *testing.T) {
		// f2 signs the token, but the provider trusts only f's keys.
		f := newFakeGoogle(t)
		f2 := newFakeGoogle(t)
		f2.claims = f2.validClaims()
		p := gt.R1(google.New(google.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://x/cb"},
			google.WithEndpoints(f2.server.URL+"/auth", f2.server.URL+"/token", f.server.URL+"/certs"))).NoError(t)
		_, err := p.Exchange(ctx, "good", "n1")
		gt.Error(t, err)
	})

	t.Run("missing inputs", func(t *testing.T) {
		f := newFakeGoogle(t)
		_, err := f.provider(t).Exchange(ctx, "", "n1")
		gt.Error(t, err)
	})
}

func TestNewValidation(t *testing.T) {
	_, err := google.New(google.Config{ClientID: "a"})
	gt.Error(t, err)
}
