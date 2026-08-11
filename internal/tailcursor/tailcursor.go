package tailcursor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	Version          = 1
	MaxSnapshotLines = 200
	MaxCursorBytes   = 4096
	AnchorLines      = 8
	CompactCursorLen = 8
	maxStoredCursors = 256
)

const compactAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"

type state struct {
	Version        int    `json:"v"`
	Scope          string `json:"s"`
	SessionDigest  string `json:"k"`
	SnapshotDigest string `json:"d"`
	SnapshotLines  int    `json:"n"`
	AnchorDigest   string `json:"a"`
	AnchorSize     int    `json:"al"`
	Offset         int    `json:"o"`
}

type Page struct {
	Text       string
	NextCursor string
	HasMore    bool
}

var cursorStore = struct {
	sync.Mutex
	values map[string]state
	order  []string
}{values: map[string]state{}}

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
	if current.SnapshotLines > len(snapshot) || current.Offset > MaxSnapshotLines {
		return Page{}, fmt.Errorf("stale tail cursor")
	}
	delta, err := deltaFor(current, snapshot)
	if err != nil {
		return Page{}, err
	}
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
	anchor := snapshot
	if len(anchor) > AnchorLines {
		anchor = anchor[len(anchor)-AnchorLines:]
	}
	return state{
		Version:        Version,
		Scope:          scope,
		SessionDigest:  digestString(session),
		SnapshotDigest: snapshotDigest(snapshot),
		SnapshotLines:  len(snapshot),
		AnchorDigest:   snapshotDigest(anchor),
		AnchorSize:     len(anchor),
		Offset:         offset,
	}
}

func deltaFor(value state, snapshot []string) ([]string, error) {
	if value.SnapshotLines <= len(snapshot) && snapshotDigest(snapshot[:value.SnapshotLines]) == value.SnapshotDigest {
		return snapshot[value.SnapshotLines:], nil
	}
	if value.AnchorSize < 1 || value.AnchorSize > len(snapshot) {
		return nil, fmt.Errorf("stale tail cursor")
	}
	matchStart := -1
	for start := 0; start+value.AnchorSize <= len(snapshot); start++ {
		if snapshotDigest(snapshot[start:start+value.AnchorSize]) == value.AnchorDigest {
			if matchStart != -1 {
				return nil, fmt.Errorf("stale tail cursor")
			}
			matchStart = start
		}
	}
	if matchStart != -1 {
		return snapshot[matchStart+value.AnchorSize:], nil
	}
	return nil, fmt.Errorf("stale tail cursor")
}

func encode(value state) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode tail cursor: %w", err)
	}
	for salt := byte(0); ; salt++ {
		encoded := compactToken(data, salt)
		cursorStore.Lock()
		previous, exists := cursorStore.values[encoded]
		if !exists || previous == value {
			if !exists {
				cursorStore.values[encoded] = value
				cursorStore.order = append(cursorStore.order, encoded)
				if len(cursorStore.order) > maxStoredCursors {
					delete(cursorStore.values, cursorStore.order[0])
					cursorStore.order = cursorStore.order[1:]
				}
			}
			cursorStore.Unlock()
			return encoded, nil
		}
		cursorStore.Unlock()
		if salt == 255 {
			return "", fmt.Errorf("encode tail cursor collision")
		}
	}
}

func decode(raw, scope, session string) (state, error) {
	if raw == "" || len(raw) > MaxCursorBytes {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	if len(raw) == CompactCursorLen && isCompact(raw) {
		cursorStore.Lock()
		value, ok := cursorStore.values[raw]
		cursorStore.Unlock()
		if !ok {
			return state{}, fmt.Errorf("invalid tail cursor")
		}
		if err := validateState(value, scope, session); err != nil {
			return state{}, err
		}
		return value, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	var value state
	if json.Unmarshal(data, &value) != nil {
		return state{}, fmt.Errorf("invalid tail cursor")
	}
	if err := validateState(value, scope, session); err != nil {
		return state{}, err
	}
	return value, nil
}

func validateState(value state, scope, session string) error {
	if value.Version != Version || value.Scope != scope || value.SessionDigest != digestString(session) || value.SnapshotLines < 0 || value.SnapshotLines > MaxSnapshotLines || value.AnchorSize < 0 || value.AnchorSize > AnchorLines || value.Offset < 0 || value.Offset > MaxSnapshotLines || value.SnapshotDigest != snapshotDigest(nil) && len(value.SnapshotDigest) != sha256.Size*2 || value.AnchorDigest != snapshotDigest(nil) && len(value.AnchorDigest) != sha256.Size*2 {
		return fmt.Errorf("invalid tail cursor")
	}
	if _, err := hex.DecodeString(value.SnapshotDigest); err != nil {
		return fmt.Errorf("invalid tail cursor")
	}
	if _, err := hex.DecodeString(value.AnchorDigest); err != nil {
		return fmt.Errorf("invalid tail cursor")
	}
	return nil
}

func isCompact(raw string) bool {
	if len(raw) != CompactCursorLen {
		return false
	}
	for i := range raw {
		if !strings.ContainsRune(compactAlphabet, rune(raw[i])) {
			return false
		}
	}
	return true
}

func compactToken(data []byte, salt byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte{salt})
	_, _ = hash.Write(data)
	digest := hash.Sum(nil)
	value := uint64(0)
	for _, b := range digest[:8] {
		value = value<<8 | uint64(b)
	}
	encoded := make([]byte, CompactCursorLen)
	for i := len(encoded) - 1; i >= 0; i-- {
		encoded[i] = compactAlphabet[value%uint64(len(compactAlphabet))]
		value /= uint64(len(compactAlphabet))
	}
	return string(encoded)
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
