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
	parseFlags()
	finalizeConfig()
	initAuth()
	loadRDB()
	setupAOF()

	if cfg.isReplica() {
		go startReplication()
	}

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("Failed to bind to port %d\n", cfg.Port)
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
	// Connections opened while the default user has no password are
	// auto-authenticated and stay that way even if a password is set later.
	c.authed = !authRequired()
	defer c.removeFromPubSub()

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
			c.send([]byte(reply))
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
	case "INFO":
		return cmdInfo(args)
	case "WAIT":
		return cmdWait(args)
	case "SELECT":
		return encodeSimpleString("OK")
	case "CONFIG":
		return cmdConfig(args)
	case "KEYS":
		return cmdKeys(args)
	case "ZADD":
		return cmdZAdd(args)
	case "ZRANK":
		return cmdZRank(args)
	case "ZRANGE":
		return cmdZRange(args)
	case "ZCARD":
		return cmdZCard(args)
	case "ZSCORE":
		return cmdZScore(args)
	case "ZREM":
		return cmdZRem(args)
	case "GEOADD":
		return cmdGeoAdd(args)
	case "GEOPOS":
		return cmdGeoPos(args)
	case "GEODIST":
		return cmdGeoDist(args)
	case "GEOSEARCH":
		return cmdGeoSearch(args)
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

func cmdConfig(args []string) string {
	if len(args) < 3 || strings.ToUpper(args[1]) != "GET" {
		return encodeError("ERR wrong number of arguments for 'config|get' command")
	}
	var parts []string
	for _, param := range args[2:] {
		name := strings.ToLower(param)
		val, ok := configValue(name)
		if !ok {
			continue
		}
		parts = append(parts, encodeBulkString(name), encodeBulkString(val))
	}
	return encodeArray(parts)
}

func configValue(name string) (string, bool) {
	switch name {
	case "dir":
		return cfg.Dir, true
	case "dbfilename":
		return cfg.DbFilename, true
	case "appendonly":
		return cfg.AppendOnly, true
	case "appenddirname":
		return cfg.AppendDirname, true
	case "appendfilename":
		return cfg.AppendFilename, true
	case "appendfsync":
		return cfg.AppendFsync, true
	default:
		return "", false
	}
}

func cmdKeys(args []string) string {
	if len(args) != 2 {
		return encodeError("ERR wrong number of arguments for 'keys' command")
	}
	keys := store.Keys()
	if args[1] == "*" {
		return encodeBulkArray(keys)
	}
	var matched []string
	for _, k := range keys {
		if globMatch(args[1], k) {
			matched = append(matched, k)
		}
	}
	return encodeBulkArray(matched)
}

// globMatch does a minimal glob: '*' matches any run, '?' one char.
func globMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	var match func(p, t string) bool
	match = func(p, t string) bool {
		for len(p) > 0 {
			switch p[0] {
			case '*':
				if match(p[1:], t) {
					return true
				}
				if len(t) == 0 {
					return false
				}
				t = t[1:]
			case '?':
				if len(t) == 0 {
					return false
				}
				p, t = p[1:], t[1:]
			default:
				if len(t) == 0 || p[0] != t[0] {
					return false
				}
				p, t = p[1:], t[1:]
			}
		}
		return len(t) == 0
	}
	return match(pattern, s)
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
