package sse

import (
	"bufio"
	"bytes"
	"io"
)

type Event struct {
	Name string
	Data []byte
}

type Reader struct {
	scanner *bufio.Scanner
}

const maxLine = 1 << 20

func NewReader(r io.Reader) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	return &Reader{scanner: scanner}
}

func (r *Reader) Next() (Event, error) {
	var event Event
	var data [][]byte
	for r.scanner.Scan() {
		line := r.scanner.Bytes()
		if len(line) == 0 {
			if len(data) == 0 && event.Name == "" {
				continue
			}
			event.Data = bytes.Join(data, []byte("\n"))
			return event, nil
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			event.Name = string(value)
		case "data":
			data = append(data, bytes.Clone(value))
		}
	}
	if err := r.scanner.Err(); err != nil {
		return Event{}, err
	}
	if len(data) > 0 || event.Name != "" {
		event.Data = bytes.Join(data, []byte("\n"))
		return event, nil
	}
	return Event{}, io.EOF
}
