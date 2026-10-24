package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/bandhanpatel/lets-go-gaffer/internal/api"
	"github.com/bandhanpatel/lets-go-gaffer/internal/service"
)

const jwt = "eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjE5OTk5OTk5OTl9.SECRET-SIGNATURE_part"

func TestViewerHeadersReachTheService(t *testing.T) {
	tests := []struct {
		name string
		hdr  map[string]string
		want service.Viewer
	}{
		{"none", nil, service.Viewer{}},
		{"team only", map[string]string{"X-Gaffer-Team": "1253022"}, service.Viewer{TeamID: 1253022}},
		{"team and a bare token", map[string]string{"X-Gaffer-Team": "42", "X-Gaffer-Token": jwt}, service.Viewer{TeamID: 42, Token: "Bearer " + jwt}},
		{"a token pasted with Bearer", map[string]string{"X-Gaffer-Token": "Bearer " + jwt}, service.Viewer{Token: "Bearer " + jwt}},
		{"lower-case bearer and stray spaces", map[string]string{"X-Gaffer-Token": "  bearer   " + jwt + " "}, service.Viewer{Token: "Bearer " + jwt}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, nil)

			w := r.do("GET", "/api/team", "", tt.hdr)

			if w.Code != 200 || r.svc.viewer != tt.want {
				t.Errorf("status %d viewer %+v, want %+v", w.Code, r.svc.viewer, tt.want)
			}
		})
	}
}

func TestMalformedViewerHeadersAre400AndNeverEchoTheToken(t *testing.T) {
	bad := []map[string]string{
		{"X-Gaffer-Team": "abc"},
		{"X-Gaffer-Team": "0"},
		{"X-Gaffer-Team": "-5"},
		{"X-Gaffer-Team": "12345678901234"},
		{"X-Gaffer-Token": "has spaces inside it"},
		{"X-Gaffer-Token": "<script>alert(1)</script>"},
		{"X-Gaffer-Token": strings.Repeat("a", 5000)},
	}
	for _, h := range bad {
		r := newRig(t, nil)
		w := r.do("GET", "/api/team", "", h)
		if w.Code != 400 {
			t.Errorf("%v: status %d", h, w.Code)
		}
		for _, v := range h {
			if len(v) > 8 && strings.Contains(w.Body.String(), v) {
				t.Errorf("an error echoed the header value back: %s", w.Body)
			}
		}
	}
}

// The token is a login for the manager's FPL account.
func TestTheTokenNeverReachesTheLogOrAnyResponse(t *testing.T) {
	var logs bytes.Buffer
	r := newRig(t, func(c *api.Config, _ *rig) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
	hdr := map[string]string{"X-Gaffer-Token": jwt, "X-Gaffer-Team": "42"}

	ok := r.do("GET", "/api/team", "", hdr)
	r.svc.err = &service.InputError{Msg: "bad club"}
	badReq := r.do("GET", "/api/team", "", hdr)
	r.svc.panics = true
	boom := r.do("GET", "/api/team", "", hdr) // a panic is logged with its stack

	for name, w := range map[string]string{"ok": ok.Body.String(), "input error": badReq.Body.String(), "panic": boom.Body.String()} {
		if strings.Contains(w, "SECRET-SIGNATURE") || strings.Contains(w, jwt) {
			t.Errorf("%s response contains the token: %s", name, w)
		}
	}
	if strings.Contains(logs.String(), "SECRET-SIGNATURE") || strings.Contains(logs.String(), "eyJhbGci") {
		t.Errorf("the log contains the token:\n%s", logs.String())
	}
}

func TestTeamRequiredHasAMachineReadableCode(t *testing.T) {
	r := newRig(t, nil)
	r.svc.err = service.ErrTeamRequired

	w := r.do("GET", "/api/lineup", "", nil)

	var body api.ErrorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 400 || body.Code != "team_required" || body.Error == "" {
		t.Errorf("%d %+v", w.Code, body)
	}
}

func TestACustomHeaderFromAnotherOriginIsRefusedBeforeItCanDoAnything(t *testing.T) {
	r := newRig(t, nil)

	w := r.do("GET", "/api/team", "", map[string]string{"Origin": "https://evil.example", "X-Gaffer-Team": "42", "X-Gaffer-Token": jwt})

	if w.Code != http.StatusForbidden || r.svc.viewer != (service.Viewer{}) {
		t.Errorf("status %d, service saw %+v", w.Code, r.svc.viewer)
	}
}

func TestAConversationBelongsToTheTeamItStartedWith(t *testing.T) {
	r := newRig(t, nil)
	first := r.do("POST", "/api/ask", `{"question":"hi"}`, map[string]string{"X-Gaffer-Team": "1"})
	var id string
	for _, e := range events(t, first.Body.String()) {
		if e.name == "session" {
			var d struct {
				ID string `json:"session_id"`
			}
			_ = json.Unmarshal([]byte(e.data), &d)
			id = d.ID
		}
	}
	body := `{"question":"again","session_id":"` + id + `"}`

	same := r.do("POST", "/api/ask", body, map[string]string{"X-Gaffer-Team": "1"})
	other := r.do("POST", "/api/ask", body, map[string]string{"X-Gaffer-Team": "2"})
	sameTeamNewToken := r.do("POST", "/api/ask", body, map[string]string{"X-Gaffer-Team": "1", "X-Gaffer-Token": jwt})

	if same.Code != 200 {
		t.Errorf("the owner's follow-up = %d", same.Code)
	}
	if other.Code != 404 || sameTeamNewToken.Code != 404 {
		t.Errorf("another team = %d, same team with a different login = %d, want 404 for both", other.Code, sameTeamNewToken.Code)
	}
}
