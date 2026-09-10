package rpc

import (
	"bytes"
	"encoding/binary"
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

func TestDecodeParamsRejectsUnknownFields(t *testing.T) {
	var params ListChatsParams
	if err := DecodeParams([]byte(`{"limit":2,"rpc":"send"}`), &params); err == nil {
		t.Fatal("unknown parameter was accepted")
	}
}
