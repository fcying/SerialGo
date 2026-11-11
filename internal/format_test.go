package internal

import (
	"bytes"
	"testing"
)

func TestEncodeModbusAndCRLF(t *testing.T) {
	got, err := Encode("01 03 00 00 00 0A", Format{Hex: true, CRLF: true, Checksum: "CRC16-Modbus", From: "1", EndSkip: "0"})
	want := []byte{1, 3, 0, 0, 0, 10, 0xc5, 0xcd, '\r', '\n'}
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Encode = % x, %v; want % x", got, err, want)
	}
}

func TestEncodeRejectsInvalidFrame(t *testing.T) {
	cases := []struct{ data, from, skip string }{{"0 A F", "1", "0"}, {"01 02", "2", "1"}, {"01 02", "1", "-1"}}
	for _, tc := range cases {
		_, err := Encode(tc.data, Format{Hex: true, Checksum: "ADD", From: tc.from, EndSkip: tc.skip})
		if err == nil {
			t.Errorf("Encode(%q, range %s / %s) accepted an invalid frame", tc.data, tc.from, tc.skip)
		}
	}
}
