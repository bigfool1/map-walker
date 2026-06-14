package synthetic

import (
	"strconv"
	"strings"
)

const UsernamePrefix = "synthetic_"

func FormatUsername(accountNumber int) string {
	return UsernamePrefix + strconv.Itoa(accountNumber)
}

func HasReservedPrefix(username string) bool {
	return strings.HasPrefix(strings.ToLower(username), UsernamePrefix)
}

func ParseUsername(username string) (accountNumber int, ok bool) {
	lower := strings.ToLower(username)
	if !strings.HasPrefix(lower, UsernamePrefix) {
		return 0, false
	}
	suffix := lower[len(UsernamePrefix):]
	if suffix == "" {
		return 0, false
	}
	if len(suffix) > 1 && suffix[0] == '0' {
		return 0, false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(suffix)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
