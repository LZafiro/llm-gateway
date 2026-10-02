package requestid

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func New() string {
	return newAt(time.Now())
}

func newAt(now time.Time) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(raw[6:])
	hi := binary.BigEndian.Uint64(raw[:8])
	lo := binary.BigEndian.Uint64(raw[8:])
	var out [26]byte
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = alphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out[:])
}
