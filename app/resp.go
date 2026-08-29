package main

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// Value represents a RESP value for replies.
type respError string

func readCommand(reader *bufio.Reader) ([]string, error) {
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, nil
	}

	if line[0] != '*' {
		return strings.Fields(line), nil
	}

	count, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		bulkLine, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		if len(bulkLine) == 0 || bulkLine[0] != '$' {
			return nil, fmt.Errorf("protocol error: expected '$'")
		}
		length, err := strconv.Atoi(bulkLine[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, length+2)
		if _, err := readFull(reader, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:length]))
	}
	return args, nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func readFull(reader *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := reader.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// Encoders.

func encodeSimpleString(s string) string { return "+" + s + "\r\n" }

func encodeError(s string) string { return "-" + s + "\r\n" }

func encodeBulkString(s string) string {
	return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n"
}

func encodeNullBulkString() string { return "$-1\r\n" }

func encodeInteger(n int64) string { return ":" + strconv.FormatInt(n, 10) + "\r\n" }

func encodeArray(items []string) string {
	var b strings.Builder
	b.WriteString("*" + strconv.Itoa(len(items)) + "\r\n")
	for _, it := range items {
		b.WriteString(it)
	}
	return b.String()
}

func encodeBulkArray(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = encodeBulkString(it)
	}
	return encodeArray(parts)
}

func encodeNullArray() string { return "*-1\r\n" }
