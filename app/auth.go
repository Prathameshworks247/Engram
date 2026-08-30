package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

// requirePass is the value of --requirepass (empty means none).
var requirePass string

// Default-user ACL state.
var (
	aclMu            sync.Mutex
	defaultPasswords []string // sha256 hex hashes
	defaultNopass    = true
)

// initAuth seeds the default user's ACL from --requirepass. Called after flags.
func initAuth() {
	if requirePass != "" {
		defaultPasswords = []string{sha256Hex(requirePass)}
		defaultNopass = false
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// authRequired reports whether connections must AUTH before running commands.
func authRequired() bool {
	aclMu.Lock()
	defer aclMu.Unlock()
	return !defaultNopass && len(defaultPasswords) > 0
}

func passwordMatches(pw string) bool {
	aclMu.Lock()
	defer aclMu.Unlock()
	if defaultNopass {
		return true
	}
	h := sha256Hex(pw)
	for _, p := range defaultPasswords {
		if p == h {
			return true
		}
	}
	return false
}

func (c *Client) currentUser() string {
	if c.user == "" {
		return "default"
	}
	return c.user
}

func (c *Client) cmdACL(args []string) string {
	if len(args) < 2 {
		return encodeError("ERR wrong number of arguments for 'acl' command")
	}
	switch strings.ToUpper(args[1]) {
	case "WHOAMI":
		return encodeBulkString(c.currentUser())
	case "GETUSER":
		return cmdACLGetUser(args)
	case "SETUSER":
		return cmdACLSetUser(args)
	default:
		return encodeError("ERR Unknown ACL subcommand or wrong number of arguments for '" + args[1] + "'")
	}
}

func cmdACLGetUser(args []string) string {
	if len(args) != 3 {
		return encodeError("ERR wrong number of arguments for 'acl|getuser' command")
	}
	if args[2] != "default" {
		return encodeNullArray()
	}
	return aclGetUserReply()
}

func aclGetUserReply() string {
	aclMu.Lock()
	defer aclMu.Unlock()

	var flags []string
	if defaultNopass {
		flags = append(flags, "nopass")
	}
	pwCopy := append([]string(nil), defaultPasswords...)

	pairs := []string{
		encodeBulkString("flags"), encodeBulkArray(flags),
		encodeBulkString("passwords"), encodeBulkArray(pwCopy),
	}
	return encodeArray(pairs)
}

func cmdACLSetUser(args []string) string {
	if len(args) < 3 {
		return encodeError("ERR wrong number of arguments for 'acl|setuser' command")
	}
	if args[2] != "default" {
		// Only the default user exists in this implementation.
		return encodeSimpleString("OK")
	}

	aclMu.Lock()
	defer aclMu.Unlock()
	for _, rule := range args[3:] {
		switch {
		case rule == "nopass":
			defaultNopass = true
			defaultPasswords = nil
		case rule == "resetpass":
			defaultNopass = false
			defaultPasswords = nil
		case strings.HasPrefix(rule, ">"):
			defaultPasswords = append(defaultPasswords, sha256Hex(rule[1:]))
			defaultNopass = false
		case strings.HasPrefix(rule, "#"):
			defaultPasswords = append(defaultPasswords, strings.ToLower(rule[1:]))
			defaultNopass = false
		case strings.HasPrefix(rule, "<"):
			h := sha256Hex(rule[1:])
			defaultPasswords = removeHash(defaultPasswords, h)
		case strings.HasPrefix(rule, "!"):
			defaultPasswords = removeHash(defaultPasswords, strings.ToLower(rule[1:]))
		case rule == "on" || rule == "off" || rule == "reset":
			// Not modelled beyond passwords; accept silently.
		}
	}
	return encodeSimpleString("OK")
}

func removeHash(list []string, h string) []string {
	out := list[:0]
	for _, p := range list {
		if p != h {
			out = append(out, p)
		}
	}
	return append([]string(nil), out...)
}

// cmdAuth handles AUTH <password> and AUTH <username> <password>.
func (c *Client) cmdAuth(args []string) string {
	var username, password string
	switch len(args) {
	case 2:
		username, password = "default", args[1]
	case 3:
		username, password = args[1], args[2]
	default:
		return encodeError("ERR wrong number of arguments for 'auth' command")
	}

	if !authRequired() {
		return encodeError("ERR Client sent AUTH, but no password is set. Did you mean AUTH <username> <password>?")
	}
	if username != "default" || !passwordMatches(password) {
		return encodeError("WRONGPASS invalid username-password pair or user is disabled.")
	}
	c.authed = true
	c.user = "default"
	return encodeSimpleString("OK")
}
