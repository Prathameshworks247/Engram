package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var store = NewStore()

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
	c := &Client{conn: conn}

	for {
		args, err := readCommand(reader)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		reply := c.dispatch(args)
		if reply != "" {
			conn.Write([]byte(reply))
		}
	}
}

func execCommand(args []string) string {
	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "PING":
		if len(args) >= 2 {
			return encodeBulkString(args[1])
		}
		return encodeSimpleString("PONG")
	case "ECHO":
		if len(args) >= 2 {
			return encodeBulkString(args[1])
		}
		return encodeError("ERR wrong number of arguments for 'echo' command")
	case "SET":
		return cmdSet(args)
	case "GET":
		return cmdGet(args)
	case "TYPE":
		if len(args) != 2 {
			return encodeError("ERR wrong number of arguments for 'type' command")
		}
		return encodeSimpleString(store.TypeOf(args[1]))
	case "RPUSH":
		return cmdRPush(args)
	case "LPUSH":
		return cmdLPush(args)
	case "LRANGE":
		return cmdLRange(args)
	case "LLEN":
		return cmdLLen(args)
	case "LPOP":
		return cmdLPop(args)
	case "BLPOP":
		return cmdBLPop(args)
	case "XADD":
		return cmdXAdd(args)
	case "XRANGE":
		return cmdXRange(args)
	case "XREAD":
		return cmdXRead(args)
	case "INCR":
		return cmdIncr(args)
	default:
		return encodeError("ERR unknown command '" + args[0] + "'")
	}
}

func cmdSet(args []string) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'set' command")
	}
	key, value := args[1], args[2]
	var ttl time.Duration
	for i := 3; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "PX":
			if i+1 >= len(args) {
				return encodeError("ERR syntax error")
			}
			ms, err := strconv.Atoi(args[i+1])
			if err != nil {
				return encodeError("ERR value is not an integer or out of range")
			}
			ttl = time.Duration(ms) * time.Millisecond
			i++
		case "EX":
			if i+1 >= len(args) {
				return encodeError("ERR syntax error")
			}
			s, err := strconv.Atoi(args[i+1])
			if err != nil {
				return encodeError("ERR value is not an integer or out of range")
			}
			ttl = time.Duration(s) * time.Second
			i++
		}
	}
	store.Set(key, value, ttl)
	return encodeSimpleString("OK")
}

func cmdGet(args []string) string {
	if len(args) < 2 {
		return encodeError("ERR wrong number of arguments for 'get' command")
	}
	value, ok := store.Get(args[1])
	if !ok {
		return encodeNullBulkString()
	}
	return encodeBulkString(value)
}
