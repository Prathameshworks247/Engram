package main

import (
	"math"
	"strconv"
	"strings"
)

const (
	geoLatMin = -85.05112878
	geoLatMax = 85.05112878
	geoLonMin = -180.0
	geoLonMax = 180.0
	geoStep   = 26
	// Redis EARTH_RADIUS_IN_METERS
	earthRadiusM = 6372797.560856
)

// geoEncode returns the 52-bit interleaved geohash for (lon, lat), matching
// Redis' geohashEncodeWGS84 + Fix52Bits.
func geoEncode(lon, lat float64) uint64 {
	latOffset := (lat - geoLatMin) / (geoLatMax - geoLatMin)
	lonOffset := (lon - geoLonMin) / (geoLonMax - geoLonMin)
	latOffset *= float64(uint64(1) << geoStep)
	lonOffset *= float64(uint64(1) << geoStep)
	return interleave64(uint32(latOffset), uint32(lonOffset))
}

// geoDecode returns the center (lon, lat) of the cell identified by bits.
func geoDecode(bits uint64) (lon, lat float64) {
	ilat, ilon := deinterleave64(bits)
	scale := float64(uint64(1) << geoStep)

	latMinCell := geoLatMin + (float64(ilat)/scale)*(geoLatMax-geoLatMin)
	latMaxCell := geoLatMin + (float64(ilat+1)/scale)*(geoLatMax-geoLatMin)
	lonMinCell := geoLonMin + (float64(ilon)/scale)*(geoLonMax-geoLonMin)
	lonMaxCell := geoLonMin + (float64(ilon+1)/scale)*(geoLonMax-geoLonMin)

	lat = (latMinCell + latMaxCell) / 2
	lon = (lonMinCell + lonMaxCell) / 2
	return
}

func interleave64(xlo, ylo uint32) uint64 {
	b := [...]uint64{
		0x5555555555555555, 0x3333333333333333, 0x0F0F0F0F0F0F0F0F,
		0x00FF00FF00FF00FF, 0x0000FFFF0000FFFF,
	}
	s := [...]uint{1, 2, 4, 8, 16}
	x := uint64(xlo)
	y := uint64(ylo)
	x = (x | (x << s[4])) & b[4]
	x = (x | (x << s[3])) & b[3]
	x = (x | (x << s[2])) & b[2]
	x = (x | (x << s[1])) & b[1]
	x = (x | (x << s[0])) & b[0]
	y = (y | (y << s[4])) & b[4]
	y = (y | (y << s[3])) & b[3]
	y = (y | (y << s[2])) & b[2]
	y = (y | (y << s[1])) & b[1]
	y = (y | (y << s[0])) & b[0]
	return x | (y << 1)
}

func deinterleave64(interleaved uint64) (x, y uint32) {
	b := [...]uint64{
		0x5555555555555555, 0x3333333333333333, 0x0F0F0F0F0F0F0F0F,
		0x00FF00FF00FF00FF, 0x0000FFFF0000FFFF, 0x00000000FFFFFFFF,
	}
	s := [...]uint{0, 1, 2, 4, 8, 16}
	xv := interleaved & b[0]
	yv := (interleaved >> 1) & b[0]
	xv = (xv | (xv >> s[1])) & b[1]
	xv = (xv | (xv >> s[2])) & b[2]
	xv = (xv | (xv >> s[3])) & b[3]
	xv = (xv | (xv >> s[4])) & b[4]
	xv = (xv | (xv >> s[5])) & b[5]
	yv = (yv | (yv >> s[1])) & b[1]
	yv = (yv | (yv >> s[2])) & b[2]
	yv = (yv | (yv >> s[3])) & b[3]
	yv = (yv | (yv >> s[4])) & b[4]
	yv = (yv | (yv >> s[5])) & b[5]
	return uint32(xv), uint32(yv)
}

func haversine(lon1, lat1, lon2, lat2 float64) float64 {
	rad := math.Pi / 180.0
	lat1r, lat2r := lat1*rad, lat2*rad
	u := math.Sin((lat2r - lat1r) / 2)
	v := math.Sin((lon2*rad - lon1*rad) / 2)
	a := u*u + math.Cos(lat1r)*math.Cos(lat2r)*v*v
	return 2.0 * earthRadiusM * math.Asin(math.Sqrt(a))
}

func validCoord(lon, lat float64) bool {
	return lon >= geoLonMin && lon <= geoLonMax && lat >= geoLatMin && lat <= geoLatMax
}

func cmdGeoAdd(args []string) string {
	if len(args) != 5 {
		return encodeError("ERR wrong number of arguments for 'geoadd' command")
	}
	key := args[1]
	lon, err1 := strconv.ParseFloat(args[2], 64)
	lat, err2 := strconv.ParseFloat(args[3], 64)
	member := args[4]
	if err1 != nil || err2 != nil {
		return encodeError("ERR value is not a valid float")
	}
	if !validCoord(lon, lat) {
		return encodeError("ERR invalid longitude,latitude pair " +
			strconv.FormatFloat(lon, 'f', 6, 64) + "," + strconv.FormatFloat(lat, 'f', 6, 64))
	}

	score := float64(geoEncode(lon, lat))

	store.Lock()
	defer store.Unlock()
	z, err := store.getOrCreateSortedSetLocked(key)
	if err != nil {
		return wrongTypeReply
	}
	added := 0
	if _, exists := z.members[member]; !exists {
		added = 1
	}
	z.members[member] = score
	store.touch(key)
	return encodeInteger(int64(added))
}

func formatCoord(f float64) string {
	return strconv.FormatFloat(f, 'f', 17, 64)
}

func cmdGeoPos(args []string) string {
	if len(args) < 2 {
		return encodeError("ERR wrong number of arguments for 'geopos' command")
	}
	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}

	parts := make([]string, 0, len(args)-2)
	for _, member := range args[2:] {
		if !ok {
			parts = append(parts, encodeNullArray())
			continue
		}
		score, exists := z.members[member]
		if !exists {
			parts = append(parts, encodeNullArray())
			continue
		}
		lon, lat := geoDecode(uint64(score))
		parts = append(parts, encodeArray([]string{
			encodeBulkString(formatCoord(lon)),
			encodeBulkString(formatCoord(lat)),
		}))
	}
	return encodeArray(parts)
}

func cmdGeoDist(args []string) string {
	if len(args) < 4 || len(args) > 5 {
		return encodeError("ERR wrong number of arguments for 'geodist' command")
	}
	unit := 1.0
	if len(args) == 5 {
		switch strings.ToLower(args[4]) {
		case "m":
			unit = 1.0
		case "km":
			unit = 1000.0
		case "mi":
			unit = 1609.34
		case "ft":
			unit = 0.3048
		default:
			return encodeError("ERR unsupported unit provided. please use M, KM, FT, MI")
		}
	}

	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(args[1])
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeNullBulkString()
	}
	s1, ok1 := z.members[args[2]]
	s2, ok2 := z.members[args[3]]
	if !ok1 || !ok2 {
		return encodeNullBulkString()
	}
	lon1, lat1 := geoDecode(uint64(s1))
	lon2, lat2 := geoDecode(uint64(s2))
	d := haversine(lon1, lat1, lon2, lat2) / unit
	return encodeBulkString(strconv.FormatFloat(d, 'f', 4, 64))
}

func cmdGeoSearch(args []string) string {
	// GEOSEARCH key FROMLONLAT <lon> <lat> BYRADIUS <radius> <unit> [ASC|DESC]
	if len(args) < 8 {
		return encodeError("ERR syntax error")
	}
	key := args[1]
	var fromLon, fromLat, radius float64
	unit := 1.0
	i := 2
	for i < len(args) {
		switch strings.ToUpper(args[i]) {
		case "FROMLONLAT":
			fromLon, _ = strconv.ParseFloat(args[i+1], 64)
			fromLat, _ = strconv.ParseFloat(args[i+2], 64)
			i += 3
		case "BYRADIUS":
			radius, _ = strconv.ParseFloat(args[i+1], 64)
			switch strings.ToLower(args[i+2]) {
			case "m":
				unit = 1.0
			case "km":
				unit = 1000.0
			case "mi":
				unit = 1609.34
			case "ft":
				unit = 0.3048
			}
			i += 3
		case "ASC", "DESC", "WITHCOORD", "WITHDIST", "WITHHASH":
			i++
		default:
			i++
		}
	}
	radiusM := radius * unit

	store.Lock()
	defer store.Unlock()
	z, ok, err := store.getSortedSetLocked(key)
	if err != nil {
		return wrongTypeReply
	}
	if !ok {
		return encodeBulkArray(nil)
	}

	type hit struct {
		member string
		dist   float64
	}
	var hits []hit
	for member, score := range z.members {
		lon, lat := geoDecode(uint64(score))
		d := haversine(fromLon, fromLat, lon, lat)
		if d <= radiusM {
			hits = append(hits, hit{member, d})
		}
	}
	// nearest first
	for a := 1; a < len(hits); a++ {
		for b := a; b > 0 && hits[b-1].dist > hits[b].dist; b-- {
			hits[b-1], hits[b] = hits[b], hits[b-1]
		}
	}
	names := make([]string, len(hits))
	for idx, h := range hits {
		names[idx] = h.member
	}
	return encodeBulkArray(names)
}
