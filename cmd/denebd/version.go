package main

import (
	"strconv"
	"strings"
)

// compareVersions orders two engine versions, semantic versions such as
// 0.6.0 or v0.6.1-beta.2: 1 if a is newer, 0 if the same, -1 if older.
// ok is false when either is not one, as test builds' names
// (m5-11092326-win) are not.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return 0, false
	}
	for i := range 3 {
		if pa.num[i] != pb.num[i] {
			return sign(pa.num[i] - pb.num[i]), true
		}
	}
	// A pre-release comes before its release (semver §11).
	switch {
	case len(pa.pre) == 0 && len(pb.pre) == 0:
		return 0, true
	case len(pa.pre) == 0:
		return 1, true
	case len(pb.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		x, y := pa.pre[i], pb.pre[i]
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx != ny {
				return sign(nx - ny), true
			}
		case ex == nil:
			return -1, true // numbers come before words
		case ey == nil:
			return 1, true
		case x != y:
			return sign(strings.Compare(x, y)), true
		}
	}
	return sign(len(pa.pre) - len(pb.pre)), true
}

type semver struct {
	num [3]int
	pre []string
}

func parseVersion(s string) (semver, bool) {
	var v semver
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+") // build metadata does not order
	s, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || len(p) > 1 && p[0] == '0' {
			return v, false
		}
		v.num[i] = n
	}
	if hasPre {
		if pre == "" {
			return v, false
		}
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" {
				return v, false
			}
		}
	}
	return v, true
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	}
	return 0
}
