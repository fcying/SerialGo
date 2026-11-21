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

func TestEncodeTextEscapes(t *testing.T) {
	tests := []struct {
		name, input string
		format      Format
		want        []byte
	}{
		{"control sequences", `AT\r\n`, Format{}, []byte("AT\r\n")},
		{"additional control sequences", `\a\b\f\v`, Format{}, []byte{'\a', '\b', '\f', '\v'}},
		{"raw null byte", `HELLO\x00`, Format{}, []byte{'H', 'E', 'L', 'L', 'O', 0}},
		{"raw FF byte", `\xFF`, Format{}, []byte{0xFF}},
		{"terminal control byte", `\x1B[2J`, Format{}, []byte{0x1B, '[', '2', 'J'}},
		{"escaped backslash", `\\r`, Format{}, []byte(`\r`)},
		{"GB18030 segmented text and raw byte", `中文\r测试\xFF结束`, Format{Encoding: "GB18030"}, []byte{0xD6, 0xD0, 0xCE, 0xC4, '\r', 0xB2, 0xE2, 0xCA, 0xD4, 0xFF, 0xBD, 0xE1, 0xCA, 0xF8}},
		{"CRLF follows escape expansion", `AT\r\n`, Format{CRLF: true}, []byte("AT\r\n\r\n")},
		{"HEX bypasses escape parsing", `5C7830`, Format{Hex: true}, []byte{'\\', 'x', '0'}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeInput(tc.input, tc.format, true)
			if err != nil || !bytes.Equal(got, tc.want) {
				t.Fatalf("encodeInput = % x, %v; want % x", got, err, tc.want)
			}
		})
	}
}

func TestEncodeTextEscapesDefaultOff(t *testing.T) {
	input := `\x41`
	got, err := Encode(input, Format{})
	if err != nil || !bytes.Equal(got, []byte(input)) {
		t.Fatalf("default Encode = % x, %v; want literal %q", got, err, input)
	}
}

func TestEncodeTextEscapesRejectInvalidSequences(t *testing.T) {
	for _, input := range []string{`\`, `\q`, `\x`, `\x0`, `\x0G`, `\xGG`} {
		got, err := encodeInput(input, Format{}, true)
		if err == nil || got != nil {
			t.Errorf("invalid escape %q produced % x, %v", input, got, err)
		}
	}
}

func TestEncodeTextEscapesPreservesIncompleteUTF8Bytes(t *testing.T) {
	input := string([]byte{0xE4, 0xB8}) + `\xFF`
	got, err := encodeInput(input, Format{}, true)
	want := []byte{0xE4, 0xB8, 0xFF}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("incomplete UTF-8 segment = % x, %v; want % x", got, err, want)
	}
}
