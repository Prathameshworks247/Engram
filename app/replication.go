package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	masterReplID     = randReplID()
	masterReplOffset int64
	offsetMu         sync.Mutex

	replicasMu sync.Mutex
	replicas   []*Replica
)

// Replica represents a connected replica from the master's point of view.
type Replica struct {
	conn      net.Conn
	mu        sync.Mutex
	ackOffset int64
}

// Base64 of a minimal valid empty RDB file (REDIS0011 + a little metadata + EOF).
const emptyRDBBase64 = "UkVESVMwMDEx+glyZWRpcy12ZXIFNy4yLjD6CnJlZGlzLWJpdHPAQP8AAAAAAAAAAA=="

func emptyRDBBytes() []byte {
	b, _ := base64.StdEncoding.DecodeString(emptyRDBBase64)
	return b
}

func randReplID() string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, 40)
	for i := range b {
		b[i] = hexdigits[rand.Intn(len(hexdigits))]
	}
	return string(b)
}

func addOffset(n int) {
	offsetMu.Lock()
	masterReplOffset += int64(n)
	offsetMu.Unlock()
}

func getOffset() int64 {
	offsetMu.Lock()
	defer offsetMu.Unlock()
	return masterReplOffset
}

var writeCommands = map[string]bool{
	"SET": true, "DEL": true, "INCR": true, "RPUSH": true, "LPUSH": true,
	"LPOP": true, "RPOP": true, "XADD": true, "EXPIRE": true, "GETDEL": true,
}

func isWriteCommand(cmd string) bool { return writeCommands[strings.ToUpper(cmd)] }

// propagate forwards a write command to every connected replica.
func propagate(args []string) {
	raw := []byte(encodeBulkArray(args))
	replicasMu.Lock()
	defer replicasMu.Unlock()
	addOffset(len(raw))
	for _, r := range replicas {
		r.conn.Write(raw)
	}
}

func registerReplica(conn net.Conn) *Replica {
	r := &Replica{conn: conn}
	replicasMu.Lock()
	replicas = append(replicas, r)
	replicasMu.Unlock()
	return r
}

func cmdInfo(args []string) string {
	section := ""
	if len(args) >= 2 {
		section = strings.ToLower(args[1])
	}
	if section != "" && section != "replication" {
		return encodeBulkString("")
	}

	role := "master"
	if cfg.isReplica() {
		role = "slave"
	}
	var b strings.Builder
	b.WriteString("# Replication\r\n")
	b.WriteString("role:" + role + "\r\n")
	b.WriteString("connected_slaves:" + strconv.Itoa(len(replicas)) + "\r\n")
	b.WriteString("master_replid:" + masterReplID + "\r\n")
	b.WriteString("master_repl_offset:" + strconv.FormatInt(getOffset(), 10) + "\r\n")
	return encodeBulkString(b.String())
}

func cmdWait(args []string) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'wait' command")
	}
	numReplicas, _ := strconv.Atoi(args[1])
	timeoutMs, _ := strconv.Atoi(args[2])

	replicasMu.Lock()
	snapshot := append([]*Replica(nil), replicas...)
	replicasMu.Unlock()

	target := getOffset()
	if target == 0 {
		return encodeInteger(int64(len(snapshot)))
	}

	getack := []byte(encodeBulkArray([]string{"REPLCONF", "GETACK", "*"}))
	for _, r := range snapshot {
		r.conn.Write(getack)
	}
	addOffset(len(getack))

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		count := 0
		for _, r := range snapshot {
			r.mu.Lock()
			a := r.ackOffset
			r.mu.Unlock()
			if a >= target {
				count++
			}
		}
		if count >= numReplicas || time.Now().After(deadline) {
			return encodeInteger(int64(count))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startReplication runs on a replica: connect to the master, perform the
// handshake, then apply the command stream.
func startReplication() {
	addr := fmt.Sprintf("%s:%d", cfg.MasterHost, cfg.MasterPort)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println("replica: failed to connect to master:", err)
		return
	}
	r := bufio.NewReader(conn)

	send := func(parts ...string) {
		conn.Write([]byte(encodeBulkArray(parts)))
	}
	readLineRaw := func() string {
		s, _ := r.ReadString('\n')
		return strings.TrimRight(s, "\r\n")
	}

	send("PING")
	readLineRaw()
	send("REPLCONF", "listening-port", strconv.Itoa(cfg.Port))
	readLineRaw()
	send("REPLCONF", "capa", "psync2")
	readLineRaw()
	send("PSYNC", "?", "-1")
	readLineRaw() // +FULLRESYNC <replid> 0

	// RDB bulk: $<len>\r\n<len bytes> (no trailing CRLF)
	hdr := readLineRaw()
	if strings.HasPrefix(hdr, "$") {
		n, _ := strconv.Atoi(hdr[1:])
		if n > 0 {
			io.ReadFull(r, make([]byte, n))
		}
	}

	var offset int64
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		n := len(encodeBulkArray(args))
		cmd := strings.ToUpper(args[0])

		if cmd == "REPLCONF" && len(args) >= 2 && strings.ToUpper(args[1]) == "GETACK" {
			// The offset reported must only cover commands processed BEFORE
			// this GETACK; add the GETACK bytes afterwards.
			conn.Write([]byte(encodeBulkArray([]string{
				"REPLCONF", "ACK", strconv.FormatInt(offset, 10),
			})))
			offset += int64(n)
			continue
		}

		execCommand(args)
		offset += int64(n)
	}
}
