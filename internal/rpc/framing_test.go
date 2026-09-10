package rpc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTripAndBounds(t *testing.T) {
	var framed bytes.Buffer
	if err := WriteFrame(&framed, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&framed, 100)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("payload = %q", got)
	}
	var oversized bytes.Buffer
	_ = binary.Write(&oversized, binary.BigEndian, uint32(101))
	if _, err := ReadFrame(&oversized, 100); err == nil {
		t.Fatal("oversized frame was accepted")
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:min(2, len(p))]) }

type stuckWriter struct{}

func (stuckWriter) Write([]byte) (int, error) { return 0, nil }

func TestFrameShortWrites(t *testing.T) {
	w := &shortWriter{}
	if err := WriteFrame(w, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&w.Buffer, 100)
	if err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("payload=%s error=%v", got, err)
	}
	if err := WriteFrame(stuckWriter{}, []byte(`{}`)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-progress error=%v", err)
	}
}

func TestDecodeParamsRejectsUnknownFields(t *testing.T) {
	var params ListChatsParams
	if err := DecodeParams([]byte(`{"limit":2,"rpc":"send"}`), &params); err == nil {
		t.Fatal("unknown parameter was accepted")
	}
}
