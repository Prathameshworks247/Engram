package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

func main() {
	l, err := net.Listen("tcp", "0.0.0.0:6379")
	if err != nil {
		fmt.Println("Failed to bind to port 6379")
		os.Exit(1)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error accepting connection: ", err.Error())
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		args, err := readCommand(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "PING":
			conn.Write([]byte("+PONG\r\n"))
		case "ECHO":
			if len(args) >= 2 {
				conn.Write([]byte(encodeBulkString(args[1])))
			}
		default:
			conn.Write([]byte("-ERR unknown command\r\n"))
		}
	}
}

// readCommand reads a single RESP command (array of bulk strings) from the reader.
func readCommand(reader *bufio.Reader) ([]string, error) {
	line, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, nil
	}

	if line[0] != '*' {
		// Inline command fallback.
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

func encodeBulkString(s string) string {
	return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n"
}
