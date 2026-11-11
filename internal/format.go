package internal

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/encoding/simplifiedchinese"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

type Format struct {
	Hex, CRLF                         bool
	Encoding, Checksum, From, EndSkip string
}

func Encode(input string, f Format) ([]byte, error) {
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
			return nil, fmt.Errorf("HEX 数据须由完整的十六进制字节组成: %w", err)
		}
	} else if f.Encoding == "GB18030" {
		data, err = simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(input))
		if err != nil {
			return nil, fmt.Errorf("编码失败: %w", err)
		}
	} else {
		data = []byte(input)
	}
	if len(data) == 0 && !f.CRLF {
		return nil, fmt.Errorf("没有要发送的数据")
	}
	if f.Checksum != "" && f.Checksum != "None" {
		from, err1 := strconv.Atoi(strings.TrimSpace(f.From))
		skip, err2 := strconv.Atoi(strings.TrimSpace(f.EndSkip))
		if err1 != nil || err2 != nil || from < 1 || skip < 0 || from > len(data)-skip {
			return nil, fmt.Errorf("校验范围无效: 起始字节从 1 开始, 末尾跳过字节数须非负")
		}
		part := data[from-1 : len(data)-skip]
		switch f.Checksum {
		case "CRC16-Modbus":
			crc := uint16(0xffff)
			for _, b := range part {
				crc ^= uint16(b)
				for range 8 {
					if crc&1 != 0 {
						crc = crc>>1 ^ 0xa001
					} else {
						crc >>= 1
					}
				}
			}
			data = append(data, byte(crc), byte(crc>>8))
		case "ADD", "XOR":
			var check byte
			for _, b := range part {
				if f.Checksum == "ADD" {
					check += b
				} else {
					check ^= b
				}
			}
			data = append(data, check)
		default:
			return nil, fmt.Errorf("未知的校验方式: %s", f.Checksum)
		}
	}
	if f.CRLF {
		data = append(data, '\r', '\n')
	}
	return data, nil
}

func Milliseconds(value, label string, min int) (time.Duration, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > 86400000 {
		return 0, fmt.Errorf("%s必须为 %d 到 86400000 ms 的整数", label, min)
	}
	return time.Duration(n) * time.Millisecond, nil
}

func Decoder(name string) transform.Transformer {
	if name == "GB18030" {
		return simplifiedchinese.GB18030.NewDecoder()
	}
	return textunicode.UTF8.NewDecoder()
}
