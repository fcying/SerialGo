package internal

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestEncodeHexAndCRLF(t *testing.T) {
	got, err := Encode("01, 03 00\n00 00 0A", Format{Hex: true, CRLF: true})
	want := []byte{1, 3, 0, 0, 0, 10, '\r', '\n'}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Encode = % x, %v; want % x", got, err, want)
	}
}

func TestEncodeRejectsIncompleteHexByte(t *testing.T) {
	_, err := Encode("0 A F", Format{Hex: true})
	if !errors.Is(err, hex.ErrLength) {
		t.Fatalf("incomplete HEX byte error identity was lost: %v", err)
	}
}

func TestEncodePreservesHexDecoderError(t *testing.T) {
	_, err := Encode("0Z", Format{Hex: true})
	var invalid hex.InvalidByteError
	if !errors.As(err, &invalid) || invalid != hex.InvalidByteError('Z') {
		t.Fatalf("HEX decoding error identity was lost: %v", err)
	}
}
