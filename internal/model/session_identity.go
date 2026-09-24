package model

import "regexp"

var canonicalSessionIDRE = regexp.MustCompile(`^(?:[A-Z]{3}_[A-Z]{3}_[PLAW]_[a-z0-9]{5}|[A-Z]{3}_ADM_[a-z0-9]{32})$`)

func IsCanonicalSessionID(value string) bool {
	return canonicalSessionIDRE.MatchString(value)
}
