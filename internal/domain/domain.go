// Package domain defines pure value types and metadata parsing for git-drev.
// It depends only on the Go standard library.
package domain

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// ZeroUUID is the placeholder UUID used on empty branch marker commits.
const ZeroUUID = "00000000-0000-0000-0000-000000000000"

var (
	// uuidStrictRegex matches a canonical RFC 4122 hexadecimal UUID string (case-insensitive).
	uuidStrictRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// uuidSubstringRegex extracts the first RFC 4122 hexadecimal UUID substring from legacy subjects.
	uuidSubstringRegex = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// RevisionMeta represents the JSON payload stored in a git-drev commit subject.
type RevisionMeta struct {
	UUID   string `json:"uuid"`
	Seq    string `json:"seq,omitempty"`
	Marker string `json:"marker,omitempty"`
}

// IsMarker reports whether the commit is an empty branch creation marker.
func (m RevisionMeta) IsMarker() bool {
	return m.Marker == "true"
}

// IsZeroUUID reports whether m.UUID equals the ZeroUUID constant (case-insensitive).
func (m RevisionMeta) IsZeroUUID() bool {
	return strings.EqualFold(m.UUID, ZeroUUID)
}

// MarshalSubject serializes RevisionMeta as a compact single-line JSON string
// suitable for a Git commit subject.
func (m RevisionMeta) MarshalSubject() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal revision metadata: %w", err)
	}
	return string(b), nil
}

// ParseRevisionMeta unmarshals a Git commit subject into a RevisionMeta struct.
// It validates that the subject is a JSON object with a valid RFC 4122 hex UUID.
func ParseRevisionMeta(subject string) (RevisionMeta, error) {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return RevisionMeta{}, fmt.Errorf("commit subject is not a JSON object")
	}
	var meta RevisionMeta
	if err := json.Unmarshal([]byte(trimmed), &meta); err != nil {
		return RevisionMeta{}, fmt.Errorf("unmarshal revision metadata: %w", err)
	}
	if !IsValidUUID(meta.UUID) {
		return RevisionMeta{}, fmt.Errorf("invalid or missing revision uuid %q", meta.UUID)
	}
	meta.UUID = strings.ToLower(meta.UUID)
	return meta, nil
}

// RemoteRevision represents a revision directory discovered on remote storage or local disk.
type RemoteRevision struct {
	UUID       string
	SizeHuman  string
	SizeBytes  int64
	ModTime    time.Time
	CommitHash string
	ShortHash  string
	CommitDate time.Time
	Refs       []string
	Meta       RevisionMeta
	RawSubject string
	Orphaned   bool
}

// IsValidUUID reports whether s is a strict 36-character RFC 4122 hex UUID (without braces).
func IsValidUUID(s string) bool {
	return uuidStrictRegex.MatchString(s)
}

// ParseUUID validates an input string as an RFC 4122 hex UUID (allowing optional
// surrounding '{' and '}' braces as supported by `git drev select`) and returns
// the normalized lowercase 36-character UUID string.
func ParseUUID(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	if !uuidStrictRegex.MatchString(trimmed) {
		return "", false
	}
	return strings.ToLower(trimmed), true
}

// ExtractFirstUUID finds the first RFC 4122 hex UUID substring in s and returns
// it normalized to lowercase, or ("", false) if none is present.
func ExtractFirstUUID(s string) (string, bool) {
	m := uuidSubstringRegex.FindString(s)
	if m == "" {
		return "", false
	}
	return strings.ToLower(m), true
}

// NewUUID generates a new lowercase RFC 4122 v4 UUID using crypto/rand.
func NewUUID() (string, error) {
	return NewUUIDFromReader(rand.Reader)
}

// NewUUIDFromReader generates a lowercase RFC 4122 v4 UUID from the provided entropy reader.
func NewUUIDFromReader(r io.Reader) (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read random bytes for uuid: %w", err)
	}
	// Set RFC 4122 version 4 (0100) and variant 10xx bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	), nil
}
