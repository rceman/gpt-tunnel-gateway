package query

import (
	"fmt"
	"strconv"
	"strings"
)

type Filter struct {
	Field  string
	Values []string
	Op     string
}

type Order struct {
	Field string
	Desc  bool
}

type Query struct {
	Entity  string
	Filters []Filter
	Select  []string
	Order   Order
	Limit   int
	Cursor  string
	Count   bool
}

func Parse(input string) (Query, error) {
	text := strings.TrimSpace(input)
	if text == "" || strings.ContainsAny(text, "{};'") {
		return Query{}, fmt.Errorf("invalid query DSL")
	}
	name, args, rest, err := call(text)
	if err != nil || !strings.HasSuffix(name, ".list") {
		return Query{}, fmt.Errorf("query must begin with <entity>.list(...)")
	}
	entity := strings.TrimSuffix(name, ".list")
	if !validName(entity) {
		return Query{}, fmt.Errorf("invalid query entity %q", entity)
	}
	q := Query{
		Entity: entity,
		Limit:  20,
	}
	if err := parseFilters(args, &q); err != nil {
		return Query{}, err
	}
	for strings.TrimSpace(rest) != "" {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, ".") {
			return Query{}, fmt.Errorf("invalid query chain")
		}
		method, value, tail, err := call(rest[1:])
		if err != nil {
			return Query{}, err
		}
		switch method {
		case "select":
			if q.Select, err = fields(value); err != nil {
				return Query{}, err
			}
		case "order_by":
			q.Order, err = order(value)
			if err != nil {
				return Query{}, err
			}
		case "limit":
			q.Limit, err = positiveInt(value)
			if err != nil {
				return Query{}, err
			}
		case "after":
			q.Cursor = strings.TrimSpace(value)
			if q.Cursor == "" {
				return Query{}, fmt.Errorf("after cursor is empty")
			}
		case "count":
			if strings.TrimSpace(value) != "" {
				return Query{}, fmt.Errorf("count takes no arguments")
			}
			q.Count = true
		default:
			return Query{}, fmt.Errorf("unsupported query function %q", method)
		}
		rest = tail
	}
	return q, nil
}

func call(input string) (string, string, string, error) {
	open := strings.IndexByte(input, '(')
	if open < 1 {
		return "", "", "", fmt.Errorf("invalid query call")
	}
	depth, close := 0, -1
	for i := open; i < len(input); i++ {
		switch input[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
				break
			}
		}
		if close >= 0 || depth < 0 {
			break
		}
	}
	if close < 0 {
		return "", "", "", fmt.Errorf("invalid query call boundaries")
	}
	return strings.TrimSpace(input[:open]), input[open+1 : close], input[close+1:], nil
}

func parseFilters(value string, q *Query) error {
	for _, part := range split(value) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, raw, ok := strings.Cut(part, "=")
		if !ok || !validName(strings.TrimSpace(key)) {
			return fmt.Errorf("invalid query filter %q", part)
		}
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if strings.Contains(raw, " or ") || strings.Contains(raw, " and ") || strings.ContainsAny(raw, "|") {
			return fmt.Errorf("boolean query expressions are not supported")
		}
		if key == "search" || key == "q" {
			q.Filters = append(q.Filters, Filter{
				Field:  "",
				Op:     "contains",
				Values: []string{unquote(raw)},
			})
			continue
		}
		values, op, err := valuesOf(raw)
		if err != nil {
			return err
		}
		q.Filters = append(q.Filters, Filter{
			Field:  key,
			Op:     op,
			Values: values,
		})
	}
	return nil
}

func valuesOf(raw string) ([]string, string, error) {
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		values := []string{}
		for _, value := range split(raw[1 : len(raw)-1]) {
			if strings.TrimSpace(value) != "" {
				values = append(values, unquote(value))
			}
		}
		if len(values) == 0 {
			return nil, "", fmt.Errorf("empty filter list")
		}
		return values, "in", nil
	}
	return []string{unquote(raw)}, "=", nil
}

func fields(value string) ([]string, error) {
	values := []string{}
	for _, item := range split(value) {
		item = strings.TrimSpace(item)
		if item == "" || item == "*" || item == "all" || !validName(item) {
			return nil, fmt.Errorf("invalid select field %q", item)
		}
		values = append(values, item)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("select requires fields")
	}
	return values, nil
}

func order(value string) (Order, error) {
	key, direction, ok := strings.Cut(value, "=")
	if !ok || !validName(strings.TrimSpace(key)) {
		return Order{}, fmt.Errorf("order_by requires field=asc|desc")
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	if direction != "asc" && direction != "desc" {
		return Order{}, fmt.Errorf("order_by direction must be asc or desc")
	}
	return Order{
		Field: strings.TrimSpace(key),
		Desc:  direction == "desc",
	}, nil
}

func positiveInt(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 || n > 100 {
		return 0, fmt.Errorf("limit must be between 1 and 100")
	}
	return n, nil
}
func validName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 && (r < 'a' || r > 'z') {
			return false
		}
		if i > 0 && !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	return true
}
func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}
func split(value string) []string {
	result, start, depth := []string{}, 0, 0
	for i, r := range value {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, value[start:i])
				start = i + 1
			}
		}
	}
	return append(result, value[start:])
}
