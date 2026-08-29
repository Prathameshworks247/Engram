package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type StreamID struct {
	Ms  uint64
	Seq uint64
}

func (id StreamID) String() string {
	return strconv.FormatUint(id.Ms, 10) + "-" + strconv.FormatUint(id.Seq, 10)
}

func (id StreamID) cmp(other StreamID) int {
	switch {
	case id.Ms < other.Ms:
		return -1
	case id.Ms > other.Ms:
		return 1
	case id.Seq < other.Seq:
		return -1
	case id.Seq > other.Seq:
		return 1
	default:
		return 0
	}
}

type StreamEntry struct {
	ID     StreamID
	Fields []string // flat [k1, v1, k2, v2, ...]
}

type Stream struct {
	entries []StreamEntry
	lastID  StreamID
}

var errStreamIDTooSmall = errors.New("ERR The ID specified in XADD is equal or smaller than the target stream top item")
var errStreamIDZero = errors.New("ERR The ID specified in XADD must be greater than 0-0")

// parseIDForXAdd parses an XADD id which may be "*", "<ms>-*", or "<ms>-<seq>".
func (st *Stream) parseIDForXAdd(raw string, nowMs uint64) (StreamID, error) {
	if raw == "*" {
		ms := nowMs
		var seq uint64
		if ms == st.lastID.Ms {
			seq = st.lastID.Seq + 1
		}
		// Guard: if there are entries and generated id is not greater, bump.
		id := StreamID{ms, seq}
		if len(st.entries) > 0 && id.cmp(st.lastID) <= 0 {
			id = StreamID{st.lastID.Ms, st.lastID.Seq + 1}
		}
		return id, nil
	}

	msPart, seqPart, hasSeq := strings.Cut(raw, "-")
	ms, err := strconv.ParseUint(msPart, 10, 64)
	if err != nil {
		return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
	}

	var seq uint64
	if !hasSeq || seqPart == "*" {
		// Auto-generate sequence.
		if len(st.entries) > 0 && ms == st.lastID.Ms {
			seq = st.lastID.Seq + 1
		} else if ms == 0 {
			seq = 1
		} else {
			seq = 0
		}
	} else {
		seq, err = strconv.ParseUint(seqPart, 10, 64)
		if err != nil {
			return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
		}
	}

	id := StreamID{ms, seq}
	if id.Ms == 0 && id.Seq == 0 {
		return StreamID{}, errStreamIDZero
	}
	if len(st.entries) > 0 && id.cmp(st.lastID) <= 0 {
		return StreamID{}, errStreamIDTooSmall
	}
	return id, nil
}

// parseRangeID parses an XRANGE bound. isStart controls seq defaulting.
func parseRangeID(raw string, isStart bool) (StreamID, error) {
	if raw == "-" {
		return StreamID{0, 0}, nil
	}
	if raw == "+" {
		return StreamID{^uint64(0), ^uint64(0)}, nil
	}
	msPart, seqPart, hasSeq := strings.Cut(raw, "-")
	ms, err := strconv.ParseUint(msPart, 10, 64)
	if err != nil {
		return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
	}
	if !hasSeq {
		if isStart {
			return StreamID{ms, 0}, nil
		}
		return StreamID{ms, ^uint64(0)}, nil
	}
	seq, err := strconv.ParseUint(seqPart, 10, 64)
	if err != nil {
		return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
	}
	return StreamID{ms, seq}, nil
}

// parseReadID parses an XREAD id (exclusive lower bound). "$" must be resolved by caller.
func parseReadID(raw string) (StreamID, error) {
	msPart, seqPart, hasSeq := strings.Cut(raw, "-")
	ms, err := strconv.ParseUint(msPart, 10, 64)
	if err != nil {
		return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
	}
	var seq uint64
	if hasSeq {
		seq, err = strconv.ParseUint(seqPart, 10, 64)
		if err != nil {
			return StreamID{}, fmt.Errorf("ERR Invalid stream ID specified as stream command argument")
		}
	}
	return StreamID{ms, seq}, nil
}

func (st *Stream) add(id StreamID, fields []string) {
	st.entries = append(st.entries, StreamEntry{ID: id, Fields: fields})
	st.lastID = id
}

// rangeEntries returns entries with start <= id <= end.
func (st *Stream) rangeEntries(start, end StreamID) []StreamEntry {
	var out []StreamEntry
	for _, e := range st.entries {
		if e.ID.cmp(start) >= 0 && e.ID.cmp(end) <= 0 {
			out = append(out, e)
		}
	}
	return out
}

// entriesAfter returns entries with id strictly greater than after.
func (st *Stream) entriesAfter(after StreamID) []StreamEntry {
	var out []StreamEntry
	for _, e := range st.entries {
		if e.ID.cmp(after) > 0 {
			out = append(out, e)
		}
	}
	return out
}
