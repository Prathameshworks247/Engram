package main

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port       int
	ReplicaOf  string // raw "host port", empty if this is a master
	MasterHost string
	MasterPort int
}

var cfg = Config{Port: 6379}

func parseFlags() {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				cfg.Port, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--replicaof":
			if i+1 < len(args) {
				spec := args[i+1]
				if strings.Contains(spec, " ") {
					parts := strings.Fields(spec)
					if len(parts) == 2 {
						cfg.MasterHost = parts[0]
						cfg.MasterPort, _ = strconv.Atoi(parts[1])
						cfg.ReplicaOf = spec
					}
					i++
				} else if i+2 < len(args) {
					cfg.MasterHost = args[i+1]
					cfg.MasterPort, _ = strconv.Atoi(args[i+2])
					cfg.ReplicaOf = args[i+1] + " " + args[i+2]
					i += 2
				} else {
					i++
				}
			}
		}
	}
}

func (c Config) isReplica() bool { return c.ReplicaOf != "" }
