package service

import (
	"fmt"
	"strings"
)

const LocalCodeMaxSearchAlternatives = 16

type codeSearchQuery struct {
	alternatives []string
	lowered      []string
	insensitive  bool
}

func parseCodeSearchQuery(query string, caseInsensitive bool) (codeSearchQuery, error) {
	if query == "" || len(query) > LocalCodeMaxQueryBytes || strings.ContainsAny(query, "\x00\r\n") {
		return codeSearchQuery{}, fmt.Errorf("invalid search query")
	}
	parsed := codeSearchQuery{insensitive: caseInsensitive}
	var current strings.Builder
	flush := func() error {
		alternative := current.String()
		if alternative == "" {
			return fmt.Errorf("search query has an empty alternative")
		}
		parsed.alternatives = append(parsed.alternatives, alternative)
		current.Reset()
		return nil
	}
	for index := 0; index < len(query); index++ {
		switch query[index] {
		case '\\':
			if index+1 >= len(query) {
				return codeSearchQuery{}, fmt.Errorf("search query has a trailing escape")
			}
			next := query[index+1]
			if next != '\\' && next != '|' {
				return codeSearchQuery{}, fmt.Errorf("search query has an unsupported escape")
			}
			current.WriteByte(next)
			index++
		case '|':
			if err := flush(); err != nil {
				return codeSearchQuery{}, err
			}
		default:
			current.WriteByte(query[index])
		}
	}
	if err := flush(); err != nil {
		return codeSearchQuery{}, err
	}
	if len(parsed.alternatives) > LocalCodeMaxSearchAlternatives {
		return codeSearchQuery{}, fmt.Errorf("search query has too many alternatives")
	}
	if caseInsensitive {
		parsed.lowered = make([]string, len(parsed.alternatives))
		for index, alternative := range parsed.alternatives {
			parsed.lowered[index] = strings.ToLower(alternative)
		}
	}
	return parsed, nil
}

func (q codeSearchQuery) matchLine(line string) (int, int) {
	haystack := line
	if q.insensitive {
		haystack = strings.ToLower(line)
	}
	bestAt, bestLen := -1, 0
	for index, alternative := range q.alternatives {
		needle := alternative
		if q.insensitive {
			needle = q.lowered[index]
		}
		at := strings.Index(haystack, needle)
		if at < 0 || (bestAt >= 0 && at >= bestAt) {
			continue
		}
		bestAt, bestLen = at, len(needle)
	}
	return bestAt, bestLen
}
