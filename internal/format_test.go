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

func TestAppendChecksumKnownVectors(t *testing.T) {
	input := []byte("123456789")
	tests := []struct {
		name, algorithm string
		want            []byte
	}{
		{"none", "None", []byte("123456789")},
		{"XOR8", "XOR8", append([]byte("123456789"), 0x31)},
		{"SUM8", "SUM8", append([]byte("123456789"), 0xDD)},
		{"SUM16-LE", "SUM16-LE", append([]byte("123456789"), 0xDD, 0x01)},
		{"SUM16-BE", "SUM16-BE", append([]byte("123456789"), 0x01, 0xDD)},
		{"Modbus CRC16", "Modbus CRC16", append([]byte("123456789"), 0x37, 0x4B)},
		{"CRC16-CCITT-FALSE", "CRC16-CCITT-FALSE", append([]byte("123456789"), 0x29, 0xB1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(nil), input...)
			got, err := appendChecksum(data, tc.algorithm, "0", "0")
			if err != nil || !bytes.Equal(got, tc.want) {
				t.Fatalf("appendChecksum = % X, %v; want % X", got, err, tc.want)
			}
		})
	}
}

func TestAppendChecksumExcludesFrameHeaderAndTail(t *testing.T) {
	data := []byte{0xAA, 0x01, 0x03, 0x00, 0xBB}
	got, err := appendChecksum(data, "XOR8", "1", "1")
	want := []byte{0xAA, 0x01, 0x03, 0x00, 0x02, 0xBB}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("header/tail exclusion = % X, %v; want % X", got, err, want)
	}
}

func TestAppendChecksumRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct {
		algorithm, header, tail string
	}{
		{"XOR8", "bad", "0"},
		{"XOR8", "-1", "0"},
		{"XOR8", "2", "2"},
		{"not-a-checksum", "0", "0"},
	} {
		got, err := appendChecksum([]byte("123"), tc.algorithm, tc.header, tc.tail)
		if err == nil || got != nil {
			t.Errorf("invalid checksum settings %+v produced % X, %v", tc, got, err)
		}
	}
	got, err := appendChecksum([]byte("123"), "None", "bad", "-1")
	if err != nil || !bytes.Equal(got, []byte("123")) {
		t.Fatalf("None should ignore exclusion counts: % X, %v", got, err)
	}
}

func TestAppendChecksumSUM16WrapsAtUint16Limit(t *testing.T) {
	data := []byte{0xFF, 0xFF, 0x02}
	tests := []struct {
		algorithm string
		want      []byte
	}{
		{"SUM16-LE", []byte{0xFF, 0xFF, 0x02, 0x00, 0x02}},
		{"SUM16-BE", []byte{0xFF, 0xFF, 0x02, 0x02, 0x00}},
	}
	for _, tc := range tests {
		got, err := appendChecksum(append([]byte(nil), data...), tc.algorithm, "0", "0")
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s uint16 wrap = % X, %v; want % X", tc.algorithm, got, err, tc.want)
		}
	}
}
