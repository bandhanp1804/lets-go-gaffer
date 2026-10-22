package fpl_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bandhanpatel/lets-go-gaffer/internal/fpl"
	"github.com/bandhanpatel/lets-go-gaffer/internal/httpx"
)

// secret is a stand-in for the bearer value copied from a logged-in browser.
const secret = "Bearer eyJhbGciOiJSUzI1NiJ9.SECRET-PAYLOAD.SECRET-SIGNATURE"

const myTeamFixture = `{
  "picks": [
    {"element": 1, "position": 1, "multiplier": 1, "is_captain": false, "is_vice_captain": false, "element_type": 1, "selling_price": 60, "purchase_price": 60},
    {"element": 9, "position": 2, "multiplier": 2, "is_captain": true,  "is_vice_captain": false, "element_type": 4, "selling_price": 156, "purchase_price": 155}
  ],
  "picks_last_updated": "2026-10-22T02:30:00Z",
  "chips": [
    {"name": "wildcard", "status_for_entry": "available", "played_by_entry": []},
    {"name": "freehit", "status_for_entry": "played", "played_by_entry": [4]}
  ],
  "transfers": {"cost": 4, "status": "cost", "limit": 1, "made": 2, "bank": 9, "value": 1003}
}`

// sessionServer serves my-team to anyone who sends the secret, and 403 otherwise.
func sessionServer(t *testing.T, seen *[]http.Header) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.Header.Clone())
		}
		if r.Header.Get("X-Api-Authorization") != secret {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"Authentication credentials were not provided."}`))
			return
		}
		_, _ = w.Write([]byte(myTeamFixture))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func hostOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

func authedClient(srv *httptest.Server, token string) *fpl.HTTPClient {
	hc := &http.Client{Transport: fpl.NewSessionTransport(nil, token, hostOf(srv))}
	return fpl.NewHTTPClientWithBaseURL(hc, srv.URL)
}

func TestFetchMyTeam_DecodesTheLiveSquad(t *testing.T) {
	var seen []http.Header
	srv := sessionServer(t, &seen)

	got, err := authedClient(srv, secret).FetchMyTeam(context.Background(), 4321)

	if err != nil {
		t.Fatal(err)
	}
	if len(got.Picks) != 2 {
		t.Fatalf("picks = %d, want 2", len(got.Picks))
	}
	want := fpl.MyPick{Element: 9, Position: 2, Multiplier: 2, IsCaptain: true, ElementType: fpl.FWD, SellingPrice: 156, PurchasePrice: 155}
	if got.Picks[1] != want {
		t.Errorf("pick = %+v, want %+v", got.Picks[1], want)
	}
	if got.Transfers != (fpl.MyTransfers{Cost: 4, Status: "cost", Limit: 1, Made: 2, Bank: 9, Value: 1003}) {
		t.Errorf("transfers = %+v", got.Transfers)
	}
	if len(got.Chips) != 2 || got.Chips[0] != (fpl.MyChip{Name: "wildcard", Status: "available"}) || got.Chips[1].Status != "played" {
		t.Errorf("chips = %+v", got.Chips)
	}
}

func TestFetchMyTeam_ARejectedSessionIsATypedErrorThatNeverLeaksTheToken(t *testing.T) {
	srv := sessionServer(t, nil)

	_, err := authedClient(srv, "Bearer expired-or-wrong").FetchMyTeam(context.Background(), 4321)

	var se *httpx.StatusError
	if !errors.Is(err, fpl.ErrSessionRejected) || !errors.As(err, &se) || se.StatusCode != 403 {
		t.Fatalf("err = %v, want ErrSessionRejected wrapping a 403", err)
	}
	for _, leak := range []string{"expired-or-wrong", "SECRET"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains the credential", err)
		}
	}
}

func TestFetchMyTeam_OtherFailuresAreNotBlamedOnTheSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(srv.Close)

	_, err := authedClient(srv, secret).FetchMyTeam(context.Background(), 4321)

	if err == nil || errors.Is(err, fpl.ErrSessionRejected) {
		t.Errorf("err = %v: a 500 is FPL's problem, not a bad session", err)
	}
}

func TestSessionTransport_SendsTheValueExactlyAsGivenOnlyToItsOwnHost(t *testing.T) {
	var ownSeen, otherSeen []http.Header
	own := sessionServer(t, &ownSeen)
	other := sessionServer(t, &otherSeen)
	tr := fpl.NewSessionTransport(nil, secret, hostOf(own))

	get := func(url string) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	get(own.URL + "/x")
	get(other.URL + "/x")

	if len(ownSeen) != 1 || ownSeen[0].Get("X-Api-Authorization") != secret {
		t.Errorf("own host saw %v, want the value exactly as configured (no prefix added or removed)", ownSeen)
	}
	if len(otherSeen) != 1 || otherSeen[0].Get("X-Api-Authorization") != "" {
		t.Errorf("another host received the credential: %v", otherSeen)
	}
}

func TestSessionTransport_ARedirectToAnotherHostDoesNotCarryTheToken(t *testing.T) {
	var targetSeen []http.Header
	target := sessionServer(t, &targetSeen)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/landed", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	tr := fpl.NewSessionTransport(nil, secret, hostOf(redirector))

	req, _ := http.NewRequest(http.MethodGet, redirector.URL+"/start", nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err == nil {
		resp.Body.Close()
	}

	if len(targetSeen) != 1 || targetSeen[0].Get("X-Api-Authorization") != "" {
		t.Errorf("the redirect target saw %v; custom headers are not stripped by Go on a cross-host redirect, so the transport must not add them", targetSeen)
	}
}

func TestSessionTransport_NoTokenAddsNothing(t *testing.T) {
	var seen []http.Header
	srv := sessionServer(t, &seen)
	tr := fpl.NewSessionTransport(nil, "", hostOf(srv))

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, _ := (&http.Client{Transport: tr}).Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	if len(seen) != 1 || seen[0].Get("X-Api-Authorization") != "" {
		t.Errorf("saw %v, want no authorization header", seen)
	}
}

// jwt builds an unsigned token whose payload carries the given expiry.
func jwt(exp int64) string {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return "Bearer " + enc(`{"alg":"none"}`) + "." + enc(fmt.Sprintf(`{"iat":%d,"exp":%d,"email":"private@example.com"}`, exp-3600, exp)) + ".sig"
}

func TestTokenExpiry(t *testing.T) {
	exp := time.Date(2026, 10, 22, 3, 28, 41, 0, time.UTC)

	tests := []struct {
		name   string
		token  string
		want   time.Time
		wantOK bool
	}{
		{"a bearer token", jwt(exp.Unix()), exp, true},
		{"the bare token without Bearer", strings.TrimPrefix(jwt(exp.Unix()), "Bearer "), exp, true},
		{"empty", "", time.Time{}, false},
		{"not a jwt", "Bearer not-a-token", time.Time{}, false},
		{"payload is not json", "Bearer aaa." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".sig", time.Time{}, false},
		{"no exp claim", "Bearer a." + base64.RawURLEncoding.EncodeToString([]byte(`{"iat":1}`)) + ".s", time.Time{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fpl.TokenExpiry(tc.token)

			if ok != tc.wantOK || !got.Equal(tc.want) {
				t.Errorf("got %v, %v; want %v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

type recordingTransport struct{ headers []http.Header }

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.headers = append(r.headers, req.Header.Clone())
	return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
}

func TestSessionTransport_NeverSendsTheTokenOverPlainHTTPToARealHost(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantSent bool
	}{
		{"https to the real host", "https://" + fpl.Host + "/api/my-team/1/", true},
		{"plain http to the real host", "http://" + fpl.Host + "/api/my-team/1/", false},
		{"plain http to localhost is allowed for tests", "http://localhost:8080/x", true},
		{"plain http to a loopback address is allowed for tests", "http://127.0.0.1:8080/x", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingTransport{}
			host := strings.TrimPrefix(strings.TrimPrefix(tc.url, "https://"), "http://")
			host = host[:strings.Index(host, "/")]
			tr := fpl.NewSessionTransport(rec, secret, host)
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)

			resp, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()

			sent := rec.headers[0].Get("X-Api-Authorization") == secret
			if sent != tc.wantSent {
				t.Errorf("token sent = %v, want %v", sent, tc.wantSent)
			}
			if req.Header.Get("X-Api-Authorization") != "" {
				t.Error("the caller's request was modified")
			}
		})
	}
}
