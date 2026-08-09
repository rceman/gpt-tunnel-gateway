package model

import "regexp"

var completionHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

var completionGateRE = regexp.MustCompile(`^G[1-9][0-9]*$`)

var completionACRE = regexp.MustCompile(`^AC[1-9][0-9]*$`)
