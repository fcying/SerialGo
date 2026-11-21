package internal

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fcying/SerialGo/internal/i18n"

	"golang.org/x/text/encoding/simplifiedchinese"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

type Format struct {
	Hex, CRLF bool
	Encoding  string
}

func Encode(input string, f Format) ([]byte, error) {
	return encodeInput(input, f, false)
}

func encodeInput(input string, f Format, parseEscapes bool) ([]byte, error) {
	data, err := encodePayload(input, f, parseEscapes)
	if err != nil {
		return nil, err
	}
	if err := validateSendData(data, f.CRLF); err != nil {
		return nil, err
	}
	if f.CRLF {
		data = append(data, '\r', '\n')
	}
	return data, nil
}

func encodePayload(input string, f Format, parseEscapes bool) ([]byte, error) {
	var data []byte
	var err error
	if f.Hex {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) || r == ',' {
				return -1
			}
			return r
		}, input)
		data, err = hex.DecodeString(clean)
		if err != nil {
			return nil, i18n.WrapError("HEX 数据须由完整的十六进制字节组成", "HEX data must consist of complete hexadecimal bytes", err)
		}
	} else if parseEscapes {
		data, err = encodeEscapedText(input, f.Encoding)
		if err != nil {
			return nil, err
		}
	} else if f.Encoding == "GB18030" {
		data, err = simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(input))
		if err != nil {
			return nil, i18n.WrapError("编码失败", "Encoding failed", err)
		}
	} else {
		data = []byte(input)
	}
	return data, nil
}

func validateSendData(data []byte, crlf bool) error {
	if len(data) == 0 && !crlf {
		return i18n.NewError("没有要发送的数据", "No data to send")
	}
	return nil
}

func appendChecksum(data []byte, algorithm, headerBytes, tailBytes string) ([]byte, error) {
	if algorithm == "" || algorithm == "None" {
		return data, nil
	}
	switch algorithm {
	case "XOR8", "SUM8", "SUM16-LE", "SUM16-BE", "Modbus CRC16", "CRC16-CCITT-FALSE":
	default:
		return nil, i18n.NewError("校验算法无效", "Invalid checksum algorithm")
	}
	header, err := checksumByteCount(headerBytes)
	if err != nil {
		return nil, err
	}
	tail, err := checksumByteCount(tailBytes)
	if err != nil {
		return nil, err
	}
	if header > len(data) || tail > len(data)-header {
		return nil, i18n.NewError("校验排除字节数超出帧长度", "Checksum excluded byte counts exceed frame length")
	}
	payload := data[header : len(data)-tail]
	var checksum [2]byte
	checksumSize := 1
	switch algorithm {
	case "XOR8":
		for _, value := range payload {
			checksum[0] ^= value
		}
	case "SUM8":
		for _, value := range payload {
			checksum[0] += value
		}
	case "SUM16-LE", "SUM16-BE":
		sum := uint16(0)
		for _, value := range payload {
			sum += uint16(value)
		}
		if algorithm == "SUM16-LE" {
			checksum[0], checksum[1] = byte(sum), byte(sum>>8)
		} else {
			checksum[0], checksum[1] = byte(sum>>8), byte(sum)
		}
		checksumSize = 2
	case "Modbus CRC16":
		value := crc16Modbus(payload)
		checksum[0], checksum[1] = byte(value), byte(value>>8)
		checksumSize = 2
	case "CRC16-CCITT-FALSE":
		value := crc16CCITTFALSE(payload)
		checksum[0], checksum[1] = byte(value>>8), byte(value)
		checksumSize = 2
	}
	insertAt := len(data) - tail
	data = append(data, checksum[:checksumSize]...)
	copy(data[insertAt+checksumSize:], data[insertAt:len(data)-checksumSize])
	copy(data[insertAt:insertAt+checksumSize], checksum[:checksumSize])
	return data, nil
}

func checksumByteCount(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	count, err := strconv.Atoi(text)
	if err != nil || count < 0 {
		return 0, i18n.NewError("校验字节数必须为非负整数", "Checksum byte counts must be non-negative integers")
	}
	return count, nil
}

func crc16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, value := range data {
		crc ^= uint16(value)
		for bit := 0; bit < 8; bit++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

func crc16CCITTFALSE(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, value := range data {
		crc ^= uint16(value) << 8
		for bit := 0; bit < 8; bit++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func encodeEscapedText(input, encoding string) ([]byte, error) {
	var source []byte
	var encoder transform.Transformer
	if encoding == "GB18030" {
		source = []byte(input)
		encoder = simplifiedchinese.GB18030.NewEncoder()
	}
	data := make([]byte, 0, len(input))
	literalStart := 0
	for i := 0; i < len(input); {
		if input[i] != '\\' {
			i++
			continue
		}
		var err error
		data, err = appendEscapedTextSegment(data, input, source, literalStart, i, encoder)
		if err != nil {
			return nil, i18n.WrapError("编码失败", "Encoding failed", err)
		}
		i++
		if i == len(input) {
			return nil, invalidTextEscapeError()
		}
		escape := input[i]
		i++
		switch escape {
		case '\\':
			data = append(data, '\\')
		case 'a':
			data = append(data, '\a')
		case 'b':
			data = append(data, '\b')
		case 'f':
			data = append(data, '\f')
		case 'v':
			data = append(data, '\v')
		case 'r':
			data = append(data, '\r')
		case 'n':
			data = append(data, '\n')
		case 't':
			data = append(data, '\t')
		case '0':
			data = append(data, 0)
		case 'x':
			if len(input)-i < 2 {
				return nil, invalidTextEscapeError()
			}
			hi, hiOK := textEscapeHexValue(input[i])
			lo, loOK := textEscapeHexValue(input[i+1])
			if !hiOK || !loOK {
				return nil, invalidTextEscapeError()
			}
			data = append(data, hi<<4|lo)
			i += 2
		default:
			return nil, invalidTextEscapeError()
		}
		literalStart = i
	}
	var err error
	data, err = appendEscapedTextSegment(data, input, source, literalStart, len(input), encoder)
	if err != nil {
		return nil, i18n.WrapError("编码失败", "Encoding failed", err)
	}
	return data, nil
}

func appendEscapedTextSegment(dst []byte, input string, source []byte, start, end int, encoder transform.Transformer) ([]byte, error) {
	if start == end {
		return dst, nil
	}
	if encoder == nil {
		return append(dst, input[start:end]...), nil
	}
	return appendGB18030(dst, encoder, source[start:end])
}

func appendGB18030(dst []byte, encoder transform.Transformer, source []byte) ([]byte, error) {
	if len(source) == 0 {
		return dst, nil
	}
	encoder.Reset()
	for {
		dst = slices.Grow(dst, len(source))
		start := len(dst)
		buffer := dst[:cap(dst)]
		nDst, nSrc, err := encoder.Transform(buffer[start:], source, true)
		dst = dst[:start+nDst]
		source = source[nSrc:]
		if err == transform.ErrShortDst {
			free := cap(dst) - len(dst)
			dst = slices.Grow(dst, free+max(len(source), 1))
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(source) == 0 {
			return dst, nil
		}
		if nSrc == 0 {
			return nil, fmt.Errorf("GB18030 encoder made no progress")
		}
	}
}

func textEscapeHexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func invalidTextEscapeError() error {
	return i18n.NewError("文本转义符无效", "Invalid text escape sequence")
}

func Milliseconds(value, label string, min int) (time.Duration, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > 86400000 {
		return 0, i18n.NewError(fmt.Sprintf("%s必须为 %d 到 86400000 ms 的整数", label, min), fmt.Sprintf("Time interval must be an integer from %d to 86400000 ms", min))
	}
	return time.Duration(n) * time.Millisecond, nil
}

func Decoder(name string) transform.Transformer {
	if name == "GB18030" {
		return simplifiedchinese.GB18030.NewDecoder()
	}
	return textunicode.UTF8.NewDecoder()
}
