package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// requirePass is the default user's password, set via --requirepass. Empty means
// no password is required (nopass).
var requirePass string

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
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

// aclGetUserReply renders `ACL GETUSER default`, built up across stages.
func aclGetUserReply() string {
	var flags []string
	if requirePass == "" {
		flags = append(flags, "nopass")
	}
	var passwords []string
	if requirePass != "" {
		passwords = append(passwords, sha256Hex(requirePass))
	}
	pairs := []string{
		encodeBulkString("flags"), encodeBulkArray(flags),
		encodeBulkString("passwords"), encodeBulkArray(passwords),
	}
	return encodeArray(pairs)
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

	if requirePass == "" {
		return encodeError("ERR Client sent AUTH, but no password is set. Did you mean AUTH <username> <password>?")
	}
	if username != "default" || password != requirePass {
		return encodeError("WRONGPASS invalid username-password pair or user is disabled.")
	}
	c.authed = true
	c.user = "default"
	return encodeSimpleString("OK")
}
