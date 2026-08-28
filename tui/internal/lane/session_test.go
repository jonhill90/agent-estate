package lane

import "testing"

func TestDecodeSessions(t *testing.T) {
	text := `{"sessions":[` +
		`{"session":"director","supervised":false,"lanes":[{"window":1,"window_id":"@5","name":"director","command":"claude.exe","state":"supervisor","idle_seconds":13}]},` +
		`{"session":"agent-supervisor","supervised":true,"lanes":[]}` +
		`],"count":2}`
	sessions, err := DecodeSessions(text)
	if err != nil {
		t.Fatalf("DecodeSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	if sessions[0].Name != "director" || sessions[0].Supervised {
		t.Fatalf("unexpected director session: %+v", sessions[0])
	}
	if len(sessions[0].Lanes) != 1 || sessions[0].Lanes[0].Name != "director" {
		t.Fatalf("director session's own lane row did not decode: %+v", sessions[0].Lanes)
	}
	if sessions[1].Name != "agent-supervisor" || !sessions[1].Supervised {
		t.Fatalf("unexpected agent-supervisor session: %+v", sessions[1])
	}
}

func TestDecodeSessionsInvalidJSON(t *testing.T) {
	if _, err := DecodeSessions("not json"); err == nil {
		t.Fatal("expected an error decoding non-JSON, got nil")
	}
}

// TestDecodeSessionsSurfacesPerSessionError is the load-bearing case:
// sessions.sh reports a session it could not read as null lanes plus an
// error string, rather than dropping it -- see sessions.sh's own module
// comment. That must decode intact, not get swallowed into an empty slice
// that looks identical to "this session genuinely has no lanes".
func TestDecodeSessionsSurfacesPerSessionError(t *testing.T) {
	text := `{"sessions":[{"session":"broken","supervised":false,"lanes":null,"error":"lanes.sh --json failed"}],"count":1}`
	sessions, err := DecodeSessions(text)
	if err != nil {
		t.Fatalf("DecodeSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Error == "" {
		t.Fatalf("expected the per-session error to survive decoding, got %+v", sessions[0])
	}
	if sessions[0].Lanes != nil {
		t.Fatalf("expected nil lanes for an unreadable session, got %+v", sessions[0].Lanes)
	}
}
