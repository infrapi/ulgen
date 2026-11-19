package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/infrapi/lib/pkg/auth"
)

func TestKayraMemberships(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/kayra.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"domain":"infrapi.io","apis":{"authorization":"/apis/authorization.infrapi.io/v1"},"endpoints":{"userinfo":"/oauth2/userinfo","check":"/apis/authorization.infrapi.io/v1/check"}}`)); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/apis/authorization.infrapi.io/v1/memberships", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer alice-token" || r.URL.Query().Get("api") != "grafana.example.io" {
			http.Error(w, `{"status":401}`, http.StatusUnauthorized)
			return
		}
		if _, err := w.Write([]byte(`[{"namespace":"acme","groups":["g"]},{"namespace":"globex","groups":["g"]}]`)); err != nil {
			t.Error(err)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ac, err := auth.NewClient(auth.ClientOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	k := NewKayraClient(ac, srv.URL, nil)
	got, err := k.Memberships(context.Background(), "alice-token", "grafana.example.io")
	if err != nil || !slices.Equal(got, []string{"acme", "globex"}) {
		t.Errorf("Memberships = %v, %v", got, err)
	}
	if _, err := k.Memberships(context.Background(), "bob-token", "grafana.example.io"); err == nil {
		t.Errorf("a refused call returned no error")
	}
}
