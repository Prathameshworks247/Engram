package main

import "errors"

var errWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")

const wrongTypeReply = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
