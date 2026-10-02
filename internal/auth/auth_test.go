package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type fakeLoader struct {
	keys []StoredKey
	err  error
}

func (f *fakeLoader) ActiveKeys(context.Context) ([]StoredKey, error) {
	return f.keys, f.err
}

func stored(plaintext string, key Key) StoredKey {
	digest := sha256.Sum256([]byte(plaintext))
	return StoredKey{Key: key, Hash: digest[:]}
}

func TestGenerate(t *testing.T) {
	plaintext, prefix, hash := Generate()
	if len(plaintext) != 35 || !strings.HasPrefix(plaintext, "gw_") || strings.Trim(plaintext[3:], alphabet) != "" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if prefix != plaintext[:10] {
		t.Errorf("prefix = %q", prefix)
	}
	if digest := sha256.Sum256([]byte(plaintext)); string(hash) != string(digest[:]) {
		t.Errorf("hash mismatch")
	}
	if other, _, _ := Generate(); other == plaintext {
		t.Errorf("generated identical keys")
	}
}

func TestLookupAfterReload(t *testing.T) {
	plaintext, _, _ := Generate()
	loader := &fakeLoader{keys: []StoredKey{stored(plaintext, Key{ID: 7, Name: "demo", Rate: 1, Burst: 3})}}
	set := NewKeySet(loader, slog.New(slog.DiscardHandler))
	if _, err := set.Lookup(plaintext); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("lookup before reload err = %v", err)
	}
	if err := set.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	key, err := set.Lookup(plaintext)
	if err != nil || key.ID != 7 || key.Name != "demo" {
		t.Fatalf("Lookup = %+v, %v", key, err)
	}
	for _, bad := range []string{"", "gw_short", "sk-" + plaintext[3:], plaintext[:34] + "!"} {
		if _, err := set.Lookup(bad); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Lookup(%q) err = %v", bad, err)
		}
	}
}

func TestRevokedKeyDisappearsOnReload(t *testing.T) {
	plaintext, _, _ := Generate()
	loader := &fakeLoader{keys: []StoredKey{stored(plaintext, Key{ID: 1})}}
	set := NewKeySet(loader, slog.New(slog.DiscardHandler))
	_ = set.Reload(t.Context())
	loader.keys = nil
	_ = set.Reload(t.Context())
	if _, err := set.Lookup(plaintext); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("revoked key still valid")
	}
}

func TestFailedReloadKeepsPreviousSet(t *testing.T) {
	plaintext, _, _ := Generate()
	loader := &fakeLoader{keys: []StoredKey{stored(plaintext, Key{ID: 1})}}
	set := NewKeySet(loader, slog.New(slog.DiscardHandler))
	_ = set.Reload(t.Context())
	loader.err = errors.New("db down")
	if err := set.Reload(t.Context()); err == nil {
		t.Fatal("want reload error")
	}
	if _, err := set.Lookup(plaintext); err != nil {
		t.Fatalf("previous set lost: %v", err)
	}
}
