package internal

import (
	"encoding/hex"
	"fmt"
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
	} else if f.Encoding == "GB18030" {
		data, err = simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(input))
		if err != nil {
			return nil, i18n.WrapError("编码失败", "Encoding failed", err)
		}
	} else {
		data = []byte(input)
	}
	if len(data) == 0 && !f.CRLF {
		return nil, i18n.NewError("没有要发送的数据", "No data to send")
	}
	if f.CRLF {
		data = append(data, '\r', '\n')
	}
	return data, nil
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
