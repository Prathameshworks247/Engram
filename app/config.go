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
	Dir        string
	DbFilename string

	AppendOnly     string
	AppendDirname  string
	AppendFilename string
	AppendFsync    string
}

var cfg = Config{
	Port:           6379,
	AppendOnly:     "no",
	AppendDirname:  "appendonlydir",
	AppendFilename: "appendonly.aof",
	AppendFsync:    "everysec",
}

func parseFlags() {
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			if i+1 < len(args) {
				cfg.Port, _ = strconv.Atoi(args[i+1])
				i++
			}
		case "--dir":
			if i+1 < len(args) {
				cfg.Dir = args[i+1]
				i++
			}
		case "--dbfilename":
			if i+1 < len(args) {
				cfg.DbFilename = args[i+1]
				i++
			}
		case "--appendonly":
			if i+1 < len(args) {
				cfg.AppendOnly = args[i+1]
				i++
			}
		case "--appenddirname":
			if i+1 < len(args) {
				cfg.AppendDirname = args[i+1]
				i++
			}
		case "--appendfilename":
			if i+1 < len(args) {
				cfg.AppendFilename = args[i+1]
				i++
			}
		case "--appendfsync":
			if i+1 < len(args) {
				cfg.AppendFsync = args[i+1]
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

// finalizeConfig fills in defaults that depend on the runtime environment.
func finalizeConfig() {
	if cfg.Dir == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.Dir = wd
		}
	}
}
