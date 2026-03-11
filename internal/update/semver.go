package update

import (
	"fmt"
	"strconv"
	"strings"
)

type semver struct {
	Major int
	Minor int
	Patch int
	Pre   []string
}

func CompareVersions(left string, right string) (int, error) {
	leftV, err := parseSemver(left)
	if err != nil {
		return 0, err
	}
	rightV, err := parseSemver(right)
	if err != nil {
		return 0, err
	}
	return compareSemver(leftV, rightV), nil
}

func parseSemver(raw string) (semver, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return semver{}, fmt.Errorf("version is empty")
	}
	trimmed = strings.TrimPrefix(strings.ToLower(trimmed), "v")
	if trimmed == "" {
		return semver{}, fmt.Errorf("version is empty")
	}

	core := trimmed
	pre := ""
	if idx := strings.Index(core, "+"); idx >= 0 {
		core = core[:idx]
	}
	if idx := strings.Index(core, "-"); idx >= 0 {
		pre = core[idx+1:]
		core = core[:idx]
	}

	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, fmt.Errorf("version %q is not semver-like", raw)
	}

	parsePart := func(value string) (int, error) {
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, fmt.Errorf("empty semver part")
		}
		return strconv.Atoi(value)
	}

	major, err := parsePart(parts[0])
	if err != nil {
		return semver{}, err
	}
	minor, err := parsePart(parts[1])
	if err != nil {
		return semver{}, err
	}
	patch := 0
	if len(parts) == 3 {
		patch, err = parsePart(parts[2])
		if err != nil {
			return semver{}, err
		}
	}

	out := semver{
		Major: major,
		Minor: minor,
		Patch: patch,
	}
	if pre != "" {
		out.Pre = strings.Split(pre, ".")
	}
	return out, nil
}

func compareSemver(left semver, right semver) int {
	if left.Major != right.Major {
		return compareInt(left.Major, right.Major)
	}
	if left.Minor != right.Minor {
		return compareInt(left.Minor, right.Minor)
	}
	if left.Patch != right.Patch {
		return compareInt(left.Patch, right.Patch)
	}

	// Release versions are greater than prerelease variants.
	if len(left.Pre) == 0 && len(right.Pre) == 0 {
		return 0
	}
	if len(left.Pre) == 0 {
		return 1
	}
	if len(right.Pre) == 0 {
		return -1
	}

	maxLen := len(left.Pre)
	if len(right.Pre) > maxLen {
		maxLen = len(right.Pre)
	}
	for i := 0; i < maxLen; i++ {
		if i >= len(left.Pre) {
			return -1
		}
		if i >= len(right.Pre) {
			return 1
		}
		leftPart := left.Pre[i]
		rightPart := right.Pre[i]
		if leftPart == rightPart {
			continue
		}
		leftNum, leftErr := strconv.Atoi(leftPart)
		rightNum, rightErr := strconv.Atoi(rightPart)
		switch {
		case leftErr == nil && rightErr == nil:
			return compareInt(leftNum, rightNum)
		case leftErr == nil && rightErr != nil:
			return -1
		case leftErr != nil && rightErr == nil:
			return 1
		default:
			if leftPart < rightPart {
				return -1
			}
			return 1
		}
	}
	return 0
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
