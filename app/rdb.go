package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RDB opcodes.
const (
	rdbOpAux          = 0xFA
	rdbOpResizeDB     = 0xFB
	rdbOpExpireTimeMS = 0xFC
	rdbOpExpireTime   = 0xFD
	rdbOpSelectDB     = 0xFE
	rdbOpEOF          = 0xFF
)

// rdbPath returns the configured RDB file path, or "" if not configured.
func rdbPath() string {
	if cfg.DbFilename == "" {
		return ""
	}
	return filepath.Join(cfg.Dir, cfg.DbFilename)
}

// loadRDB reads the configured RDB file into the store. A missing file is not
// an error (fresh dataset).
func loadRDB() {
	path := rdbPath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("rdb: open:", err)
		}
		return
	}
	defer f.Close()

	r := bufio.NewReader(f)
	header := make([]byte, 9)
	if _, err := io.ReadFull(r, header); err != nil {
		fmt.Println("rdb: header:", err)
		return
	}
	if string(header[:5]) != "REDIS" {
		fmt.Println("rdb: bad magic")
		return
	}

	var pendingExpireMs int64
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case rdbOpEOF:
			return
		case rdbOpSelectDB:
			if _, err := rdbReadLength(r); err != nil {
				return
			}
		case rdbOpResizeDB:
			if _, err := rdbReadLength(r); err != nil {
				return
			}
			if _, err := rdbReadLength(r); err != nil {
				return
			}
		case rdbOpAux:
			if _, err := rdbReadString(r); err != nil {
				return
			}
			if _, err := rdbReadString(r); err != nil {
				return
			}
		case rdbOpExpireTime:
			buf := make([]byte, 4)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			pendingExpireMs = int64(binary.LittleEndian.Uint32(buf)) * 1000
		case rdbOpExpireTimeMS:
			buf := make([]byte, 8)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			pendingExpireMs = int64(binary.LittleEndian.Uint64(buf))
		default:
			// b is a value-type byte. Only string (0) is supported.
			key, err := rdbReadString(r)
			if err != nil {
				return
			}
			val, err := rdbReadString(r)
			if err != nil {
				return
			}
			if b == 0 {
				store.SetAbsolute(key, val, pendingExpireMs)
			}
			pendingExpireMs = 0
		}
	}
}

// rdbReadLength decodes a length-encoded integer. The bool return reports
// whether the value was an "encoded" special (only relevant to strings).
func rdbReadLength(r *bufio.Reader) (uint64, error) {
	n, _, err := rdbReadLengthEncoded(r)
	return n, err
}

func rdbReadLengthEncoded(r *bufio.Reader) (length uint64, encoded bool, err error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, false, err
	}
	switch b >> 6 {
	case 0: // 00xxxxxx: 6-bit length
		return uint64(b & 0x3F), false, nil
	case 1: // 01xxxxxx yyyyyyyy: 14-bit length
		b2, err := r.ReadByte()
		if err != nil {
			return 0, false, err
		}
		return uint64(b&0x3F)<<8 | uint64(b2), false, nil
	case 2: // 10......: 32-bit or 64-bit length follows
		if b == 0x80 {
			buf := make([]byte, 4)
			if _, err := io.ReadFull(r, buf); err != nil {
				return 0, false, err
			}
			return uint64(binary.BigEndian.Uint32(buf)), false, nil
		}
		if b == 0x81 {
			buf := make([]byte, 8)
			if _, err := io.ReadFull(r, buf); err != nil {
				return 0, false, err
			}
			return binary.BigEndian.Uint64(buf), false, nil
		}
		return 0, false, fmt.Errorf("rdb: unknown length prefix %#x", b)
	default: // 11xxxxxx: special encoding, low 6 bits say which
		return uint64(b & 0x3F), true, nil
	}
}

// rdbReadString decodes a string, handling both plain and integer encodings.
func rdbReadString(r *bufio.Reader) (string, error) {
	length, encoded, err := rdbReadLengthEncoded(r)
	if err != nil {
		return "", err
	}
	if encoded {
		switch length {
		case 0: // int8
			b, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", int8(b)), nil
		case 1: // int16 LE
			buf := make([]byte, 2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", int16(binary.LittleEndian.Uint16(buf))), nil
		case 2: // int32 LE
			buf := make([]byte, 4)
			if _, err := io.ReadFull(r, buf); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", int32(binary.LittleEndian.Uint32(buf))), nil
		default:
			return "", fmt.Errorf("rdb: unsupported string encoding %d", length)
		}
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
