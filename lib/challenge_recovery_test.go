package lib

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TecharoHQ/anubis"
	"github.com/TecharoHQ/anubis/internal"
	"github.com/TecharoHQ/anubis/lib/challenge"
	"github.com/TecharoHQ/anubis/lib/store"
	dto "github.com/prometheus/client_model/go"
	"golang.org/x/net/html"
)

type challengeReadFailureStore struct{ store.Interface }

func (s challengeReadFailureStore) Get(ctx context.Context, key string) ([]byte, error) {
	if strings.HasPrefix(key, "challenge:") {
		return nil, fmt.Errorf("read failed: %w", store.ErrCantDecode)
	}
	return s.Interface.Get(ctx, key)
}

func challengeRejectionCount(t *testing.T, reason string) float64 {
	t.Helper()
	var metric dto.Metric
	if err := challengeRejections.WithLabelValues(reason).Write(&metric); err != nil {
		t.Fatal(err)
	}
	return metric.GetCounter().GetValue()
}

func recoveryLink(t *testing.T, body string) string {
	t.Helper()
	node, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var visit func(*html.Node) string
	visit = func(n *html.Node) string {
		var id, href string
		for _, attr := range n.Attr {
			if attr.Key == "id" {
				id = attr.Val
			}
			if attr.Key == "href" {
				href = attr.Val
			}
		}
		if n.Data == "a" && id == "anubis-retry" {
			return href
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if result := visit(child); result != "" {
				return result
			}
		}
		return ""
	}
	return visit(node)
}

func TestChallengeRecovery(t *testing.T) {
	const target = "/away.php?partner=example&url=https%3A%2F%2Fexample.org%2Fcourse%3Fa%3D1%26b%3D2"
	for _, tt := range []struct {
		name   string
		status int
		reason string
	}{
		{"fresh", http.StatusFound, ""},
		{"spent", http.StatusConflict, "double_spend"},
		{"missing", http.StatusGone, "unavailable"},
		{"expired", http.StatusGone, "unavailable"},
		{"read failure", http.StatusInternalServerError, ""},
		{"write failure", http.StatusInternalServerError, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := spawnAnubis(t, Options{Next: http.NewServeMux(), Policy: loadPolicies(t, "testdata/zero_difficulty.yaml", 0)})
			ts := httptest.NewServer(internal.RemoteXRealIP(true, "tcp", srv))
			t.Cleanup(ts.Close)
			c := makeChallenge(t, ts, httpClient(t))
			j := store.JSON[challenge.Challenge]{Underlying: srv.store}
			stored, err := j.Get(t.Context(), "challenge:"+c.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch tt.name {
			case "spent":
				stored.Spent = true
				err = j.Set(t.Context(), "challenge:"+c.ID, stored, time.Minute)
			case "missing":
				err = srv.store.Delete(t.Context(), "challenge:"+c.ID)
			case "expired":
				err = j.Set(t.Context(), "challenge:"+c.ID, stored, -time.Second)
			case "read failure":
				srv.store = challengeReadFailureStore{srv.store}
			case "write failure":
				srv.store = spendFailureStore{srv.store}
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, lang := range []string{"ru", "en", "de"} {
				for _, withCookie := range []bool{true, false} {
					// Fresh validation and spend writes require a verification cookie.
					if (tt.name == "fresh" || tt.name == "write failure") && (!withCookie || lang != "ru") {
						continue
					}
					q := url.Values{"redir": {target}, "id": {c.ID}, "nonce": {"0"}, "elapsedTime": {"1"}, "response": {internal.SHA256sum(stored.RandomData + "0")}}
					req := httptest.NewRequest("GET", "/?"+q.Encode(), nil)
					req.Header.Set("X-Real-IP", "192.0.2.1")
					req.Header.Set("Accept-Language", lang)
					if withCookie {
						req.AddCookie(&http.Cookie{Name: srv.cookieName(anubis.TestCookieName), Value: c.ID})
					}
					before := challengeRejectionCount(t, tt.reason)
					rec := httptest.NewRecorder()
					srv.PassChallenge(rec, req)
					if rec.Code != tt.status {
						t.Fatalf("%s cookie=%v: status %d, want %d", lang, withCookie, rec.Code, tt.status)
					}
					if got := authCookie(srv, rec.Result()) != nil; got != (tt.name == "fresh") {
						t.Fatalf("unexpected clearance cookie: %v", got)
					}
					if tt.reason != "" {
						if got := recoveryLink(t, rec.Body.String()); got != target {
							t.Fatalf("recovery query changed: %q", got)
						}
						if challengeRejectionCount(t, tt.reason) != before+1 {
							t.Error("rejection metric not incremented")
						}
						if rec.Header().Get("Location") != "" || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
							t.Error("recovery must not redirect or permit caching")
						}
					}
				}
			}
		})
	}
}

func TestChallengeRecoveryURL(t *testing.T) {
	req := httptest.NewRequest("GET", "https://ihclick.ru/", nil)
	for _, tt := range []struct{ target, want string }{
		{"/away.php?a=1&b=2", "/away.php?a=1&b=2"},
		{"https://ihclick.ru/away.php?a=%2F", "/away.php?a=%2F"},
		{"https://example.org/course", ""},
		{"/.within.website/x/cmd/anubis/api/pass-challenge", ""},
		{"/a/../.within.website/x/cmd/anubis/api/pass-challenge", ""},
		{"/%2e%2e//example.org/course", ""},
		{"//example.org/course", ""},
		{"/%2Fexample.org/course", ""},
		{"/%5Cexample.org/course", ""},
	} {
		t.Run(tt.target, func(t *testing.T) {
			target, err := url.Parse(tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if got := challengeRecoveryURL(req, target); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
