package tailcursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	Version          = 1
	MaxSnapshotLines = 200
	MaxCursorBytes   = 4096
)

type state struct {
	Version        int    `json:"v"`
	Scope          string `json:"s"`
	SessionDigest  string `json:"k"`
	SnapshotDigest string `json:"d"`
	SnapshotLines  int    `json:"n"`
	Offset         int    `json:"o"`
}

type Page struct {
	Text       string
	NextCursor string
	HasMore    bool
}

func Initial(scope, session string, snapshot []string, lines, skip int) (Page, error) {
	if err := validateBounds(snapshot, lines, skip); err != nil {
		return Page{}, err
	}
	if skip > len(snapshot) {
		return Page{}, fmt.Errorf("tail skip exceeds available output")
	}
	end := len(snapshot) - skip
	start := end - lines
	if start < 0 {
		start = 0
	}
	next, err := encode(stateFor(scope, session, snapshot, 0))
	if err != nil {
		return Page{}, err
	}
	return Page{
		Text:       joinLines(snapshot[start:end]),
		NextCursor: next,
	}, nil
}

func Continue(scope, session, raw string, snapshot []string, lines int) (Page, error) {
	if lines < 1 || lines > MaxSnapshotLines {
		return Page{}, fmt.Errorf("invalid tail line count")
	}
	current, err := decode(raw, scope, session)
	if err != nil {
		return Page{}, err
	}
	if current.SnapshotLines > len(snapshot) || current.Offset > len(snapshot) {
		return Page{}, fmt.Errorf("stale tail cursor")
	}
	if current.SnapshotLines > 0 {
		prefix := snapshot[:current.SnapshotLines]
		if snapshotDigest(prefix) != current.SnapshotDigest {
			return Page{}, fmt.Errorf("stale tail cursor")
		}
	} else if current.SnapshotDigest != snapshotDigest(nil) {
		return Page{}, fmt.Errorf("stale tail cursor")
	}
	delta := snapshot[current.SnapshotLines:]
	if current.Offset > len(delta) {
		return Page{}, fmt.Errorf("stale tail cursor")
	}
	start := current.Offset
	end := start + lines
	if end > len(delta) {
		end = len(delta)
	}
	nextState := current
	nextState.Offset = end
	if end == len(delta) {
		nextState = stateFor(scope, session, snapshot, 0)
	}
	next, err := encode(nextState)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Text:       joinLines(delta[start:end]),
		NextCursor: next,
		HasMore:    end < len(delta),
	}, nil
}

func validateBounds(snapshot []string, lines, skip int) error {
	if len(snapshot) > MaxSnapshotLines || lines < 1 || lines > MaxSnapshotLines || skip < 0 || lines+skip > MaxSnapshotLines {
		return fmt.Errorf("invalid tail bounds")
	}
	return nil
}

func stateFor(scope, session string, snapshot []string, offset int) state {
	return state{
		Version:        Version,
		Scope:          scope,
		SessionDigest:  digestString(session),
		SnapshotDigest: snapshotDigest(snapshot),
		SnapshotLines:  len(snapshot),
		Offset:         offset,
	}
}

func encode(value state) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode tail cursor: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > MaxCursorBytes {
		return "", fmt.Errorf("tail cursor exceeds limit")
	}
	return encoded, nil
}

func decode(raw, scope, session string) (state, error) {
	if raw == "" || len(raw) > MaxCursorBytes {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	var value state
	if json.Unmarshal(data, &value) != nil || value.Version != Version || value.Scope != scope || value.SessionDigest != digestString(session) || value.SnapshotLines < 0 || value.SnapshotLines > MaxSnapshotLines || value.Offset < 0 || value.Offset > MaxSnapshotLines || value.SnapshotDigest != snapshotDigest(nil) && len(value.SnapshotDigest) != sha256.Size*2 {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	if _, err := hex.DecodeString(value.SnapshotDigest); err != nil {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	return value, nil
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func snapshotDigest(lines []string) string {
	value := strings.Join(lines, "\x00")
	return digestString(value)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
