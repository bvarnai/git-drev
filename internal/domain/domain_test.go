package domain_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"git-drev/internal/domain"
)

func TestUUIDValidationAndParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantValid bool
		wantParse bool
		wantUUID  string
	}{
		{
			name:      "valid lowercase uuid v4",
			input:     "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
			wantValid: true,
			wantParse: true,
			wantUUID:  "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
		},
		{
			name:      "valid uppercase uuid",
			input:     "C1CA82B1-7F34-4F4C-9A76-05E3297B2A23",
			wantValid: true,
			wantParse: true,
			wantUUID:  "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
		},
		{
			name:      "braced uuid allowed by ParseUUID",
			input:     "{C1CA82B1-7F34-4F4C-9A76-05E3297B2A23}",
			wantValid: false,
			wantParse: true,
			wantUUID:  "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
		},
		{
			name:      "zero uuid",
			input:     domain.ZeroUUID,
			wantValid: true,
			wantParse: true,
			wantUUID:  domain.ZeroUUID,
		},
		{
			name:      "reject non-hex letters G-Z (legacy bug fix)",
			input:     "g1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
			wantValid: false,
			wantParse: false,
		},
		{
			name:      "reject mismatched braces",
			input:     "{c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
			wantValid: false,
			wantParse: false,
		},
		{
			name:      "reject branch name",
			input:     "feature/login",
			wantValid: false,
			wantParse: false,
		},
		{
			name:      "reject empty string",
			input:     "",
			wantValid: false,
			wantParse: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := domain.IsValidUUID(tc.input); got != tc.wantValid {
				t.Errorf("IsValidUUID(%q) = %v, want %v", tc.input, got, tc.wantValid)
			}
			gotUUID, ok := domain.ParseUUID(tc.input)
			if ok != tc.wantParse {
				t.Errorf("ParseUUID(%q) ok = %v, want %v", tc.input, ok, tc.wantParse)
			}
			if ok && gotUUID != tc.wantUUID {
				t.Errorf("ParseUUID(%q) = %q, want %q", tc.input, gotUUID, tc.wantUUID)
			}
		})
	}
}

func TestExtractFirstUUID(t *testing.T) {
	t.Parallel()

	u, ok := domain.ExtractFirstUUID(`legacy commit subject with C1CA82B1-7F34-4F4C-9A76-05E3297B2A23 embedded`)
	if !ok || u != "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23" {
		t.Errorf("ExtractFirstUUID got (%q, %v), want lowercase uuid", u, ok)
	}
	if _, ok := domain.ExtractFirstUUID("Add initial drev configuration"); ok {
		t.Errorf("expected no uuid in initial commit subject")
	}
}

func TestRevisionMetaMarshalAndParse(t *testing.T) {
	t.Parallel()

	t.Run("standard put revision commit", func(t *testing.T) {
		meta := domain.RevisionMeta{
			UUID: "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23",
			Seq:  "1700000000",
		}
		subj, err := meta.MarshalSubject()
		if err != nil {
			t.Fatalf("MarshalSubject error: %v", err)
		}
		want := `{"uuid":"c1ca82b1-7f34-4f4c-9a76-05e3297b2a23","seq":"1700000000"}`
		if subj != want {
			t.Errorf("MarshalSubject() = %q, want %q", subj, want)
		}

		parsed, err := domain.ParseRevisionMeta(subj)
		if err != nil {
			t.Fatalf("ParseRevisionMeta error: %v", err)
		}
		if parsed.UUID != meta.UUID || parsed.Seq != meta.Seq || parsed.IsMarker() || parsed.IsZeroUUID() {
			t.Errorf("unexpected parsed meta: %+v", parsed)
		}
	})

	t.Run("branch creation marker commit with zero uuid", func(t *testing.T) {
		meta := domain.RevisionMeta{
			UUID:   domain.ZeroUUID,
			Marker: "true",
		}
		subj, err := meta.MarshalSubject()
		if err != nil {
			t.Fatalf("MarshalSubject error: %v", err)
		}
		want := `{"uuid":"00000000-0000-0000-0000-000000000000","marker":"true"}`
		if subj != want {
			t.Errorf("MarshalSubject() = %q, want %q", subj, want)
		}

		parsed, err := domain.ParseRevisionMeta(subj)
		if err != nil {
			t.Fatalf("ParseRevisionMeta error: %v", err)
		}
		if !parsed.IsMarker() || !parsed.IsZeroUUID() {
			t.Errorf("expected marker and zero uuid to be true, got %+v", parsed)
		}
	})

	t.Run("uppercase uuid in JSON is normalized to lowercase", func(t *testing.T) {
		parsed, err := domain.ParseRevisionMeta(`{"uuid":"C1CA82B1-7F34-4F4C-9A76-05E3297B2A23","seq":"1"}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed.UUID != "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23" {
			t.Errorf("got %q, want lowercase", parsed.UUID)
		}
	})

	t.Run("invalid subjects fail ParseRevisionMeta", func(t *testing.T) {
		invalid := []string{
			"",
			"Add initial drev configuration",
			`{"uuid":"not-a-uuid","seq":"1"}`,
			`{"seq":"1"}`,
			`{malformed json`,
		}
		for _, s := range invalid {
			if _, err := domain.ParseRevisionMeta(s); err == nil {
				t.Errorf("ParseRevisionMeta(%q) succeeded, expected error", s)
			}
		}
	})
}

func TestNewUUID(t *testing.T) {
	t.Parallel()

	u1, err := domain.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID error: %v", err)
	}
	u2, err := domain.NewUUID()
	if err != nil {
		t.Fatalf("NewUUID error: %v", err)
	}
	if !domain.IsValidUUID(u1) || u1 != strings.ToLower(u1) {
		t.Errorf("NewUUID returned invalid or non-lowercase uuid: %q", u1)
	}
	if u1 == u2 {
		t.Errorf("expected two generated UUIDs to be distinct, both were %q", u1)
	}
	// Check version 4 nibble (index 14) and variant nibble (index 19).
	if u1[14] != '4' {
		t.Errorf("expected version 4 nibble '4' at index 14, got %q in %q", u1[14], u1)
	}
	if !strings.ContainsRune("89ab", rune(u1[19])) {
		t.Errorf("expected RFC 4122 variant nibble at index 19, got %q in %q", u1[19], u1)
	}

	// Deterministic reader & short-read error test.
	_, err = domain.NewUUIDFromReader(bytes.NewReader([]byte{1, 2, 3}))
	if err == nil || errors.Is(err, nil) {
		t.Errorf("expected error on short reader")
	}
}
