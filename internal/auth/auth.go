package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
)

const (
	Prefix       = "gw_"
	secretLength = 32
	prefixLength = 10
	alphabet     = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var ErrInvalidKey = errors.New("invalid api key")

type Key struct {
	ID     int64
	Name   string
	Prefix string
	Rate   float64
	Burst  int
}

type StoredKey struct {
	Key
	Hash []byte
}

type Loader interface {
	ActiveKeys(ctx context.Context) ([]StoredKey, error)
}

func Generate() (plaintext, displayPrefix string, hash []byte) {
	plaintext = Prefix + randomSecret()
	digest := sha256.Sum256([]byte(plaintext))
	return plaintext, plaintext[:prefixLength], digest[:]
}

func randomSecret() string {
	const unbiased = 256 - 256%len(alphabet)
	out := make([]byte, 0, secretLength)
	buf := make([]byte, secretLength)
	for len(out) < secretLength {
		_, _ = rand.Read(buf)
		for _, v := range buf {
			if int(v) < unbiased && len(out) < secretLength {
				out = append(out, alphabet[int(v)%len(alphabet)])
			}
		}
	}
	return string(out)
}

type KeySet struct {
	loader Loader
	logger *slog.Logger
	keys   atomic.Pointer[map[[32]byte]Key]
}

func NewKeySet(loader Loader, logger *slog.Logger) *KeySet {
	s := &KeySet{loader: loader, logger: logger}
	empty := map[[32]byte]Key{}
	s.keys.Store(&empty)
	return s
}

func (s *KeySet) Lookup(plaintext string) (Key, error) {
	if !strings.HasPrefix(plaintext, Prefix) || len(plaintext) != len(Prefix)+secretLength {
		return Key{}, ErrInvalidKey
	}
	key, ok := (*s.keys.Load())[sha256.Sum256([]byte(plaintext))]
	if !ok {
		return Key{}, ErrInvalidKey
	}
	return key, nil
}

func (s *KeySet) Reload(ctx context.Context) error {
	stored, err := s.loader.ActiveKeys(ctx)
	if err != nil {
		return fmt.Errorf("load api keys: %w", err)
	}
	next := make(map[[32]byte]Key, len(stored))
	for _, k := range stored {
		if len(k.Hash) != sha256.Size {
			continue
		}
		next[[32]byte(k.Hash)] = k.Key
	}
	s.keys.Store(&next)
	return nil
}

func (s *KeySet) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Reload(ctx); err != nil && ctx.Err() == nil {
				s.logger.WarnContext(ctx, "api key reload failed, keeping previous set", "error", err)
			}
		}
	}
}
