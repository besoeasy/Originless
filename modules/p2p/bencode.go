package p2p

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
)

var (
	ErrBencodeInvalid = errors.New("invalid bencode data")
)

// BencodeEncode serializes Go primitive types (string, []byte, int, int64, []any, map[string]any) to bencode bytes.
func BencodeEncode(val any) ([]byte, error) {
	var buf bytes.Buffer
	if err := bencodeWrite(&buf, val); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func bencodeWrite(buf *bytes.Buffer, val any) error {
	switch v := val.(type) {
	case string:
		buf.WriteString(strconv.Itoa(len(v)))
		buf.WriteByte(':')
		buf.WriteString(v)
	case []byte:
		buf.WriteString(strconv.Itoa(len(v)))
		buf.WriteByte(':')
		buf.Write(v)
	case int:
		buf.WriteByte('i')
		buf.WriteString(strconv.Itoa(v))
		buf.WriteByte('e')
	case int64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(v, 10))
		buf.WriteByte('e')
	case uint16:
		buf.WriteByte('i')
		buf.WriteString(strconv.Itoa(int(v)))
		buf.WriteByte('e')
	case []any:
		buf.WriteByte('l')
		for _, item := range v {
			if err := bencodeWrite(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case map[string]any:
		buf.WriteByte('d')
		// Keys must be sorted in bencode dictionaries
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// Write key
			buf.WriteString(strconv.Itoa(len(k)))
			buf.WriteByte(':')
			buf.WriteString(k)
			// Write value
			if err := bencodeWrite(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("unsupported bencode type: %T", val)
	}
	return nil
}

// BencodeDecode parses bencoded bytes into generic Go types:
// string (stored as string), integer (stored as int64), list ([]any), dictionary (map[string]any).
func BencodeDecode(data []byte) (any, error) {
	r := bytes.NewReader(data)
	val, err := bencodeRead(r)
	if err != nil {
		return nil, err
	}
	return val, nil
}

func bencodeRead(r *bytes.Reader) (any, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err
	}

	switch {
	case b == 'i':
		// Integer: i<digits>e
		var digits []byte
		for {
			ch, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if ch == 'e' {
				break
			}
			digits = append(digits, ch)
		}
		num, err := strconv.ParseInt(string(digits), 10, 64)
		if err != nil {
			return nil, ErrBencodeInvalid
		}
		return num, nil

	case b == 'l':
		// List: l<items>e
		var list []any
		for {
			ch, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if ch == 'e' {
				break
			}
			if err := r.UnreadByte(); err != nil {
				return nil, err
			}
			item, err := bencodeRead(r)
			if err != nil {
				return nil, err
			}
			list = append(list, item)
		}
		return list, nil

	case b == 'd':
		// Dictionary: d<key1><val1>...e
		dict := make(map[string]any)
		for {
			ch, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if ch == 'e' {
				break
			}
			if err := r.UnreadByte(); err != nil {
				return nil, err
			}
			// Key must be string
			keyVal, err := bencodeRead(r)
			if err != nil {
				return nil, err
			}
			keyStr, ok := keyVal.(string)
			if !ok {
				return nil, ErrBencodeInvalid
			}
			val, err := bencodeRead(r)
			if err != nil {
				return nil, err
			}
			dict[keyStr] = val
		}
		return dict, nil

	case b >= '0' && b <= '9':
		// String: <length>:<bytes>
		var lenDigits []byte
		lenDigits = append(lenDigits, b)
		for {
			ch, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			if ch == ':' {
				break
			}
			if ch < '0' || ch > '9' {
				return nil, ErrBencodeInvalid
			}
			lenDigits = append(lenDigits, ch)
		}
		strLen, err := strconv.Atoi(string(lenDigits))
		if err != nil || strLen < 0 {
			return nil, ErrBencodeInvalid
		}
		strBytes := make([]byte, strLen)
		if _, err := io.ReadFull(r, strBytes); err != nil {
			return nil, err
		}
		return string(strBytes), nil

	default:
		return nil, ErrBencodeInvalid
	}
}
