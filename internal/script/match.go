// SPDX-License-Identifier: MIT

package script

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lookup returns the value at a path such as "idTokenInfo.status" or
// "getVariableResult[0].attributeStatus" (also "getVariableResult.0.attributeStatus").
func lookup(doc any, path string) (any, bool) {
	cur := doc
	for _, part := range splitPath(path) {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

func splitPath(path string) []string {
	path = strings.ReplaceAll(strings.ReplaceAll(path, "[", "."), "]", "")
	var parts []string
	for _, p := range strings.Split(path, ".") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// canonical is the JSON form of a value, so that YAML's 300 equals JSON's 300.0.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var x any
	if json.Unmarshal(b, &x) == nil {
		b, _ = json.Marshal(x)
	}
	return string(b)
}

// check evaluates every condition on doc and returns the failures, sorted by path.
func check(raw json.RawMessage, expect map[string]Matcher) []string {
	var doc any
	_ = json.Unmarshal(raw, &doc)
	paths := make([]string, 0, len(expect))
	for p := range expect {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var fails []string
	for _, p := range paths {
		m := expect[p]
		got, ok := lookup(doc, p)
		switch {
		case m.Present != nil:
			if ok != *m.Present {
				if ok {
					fails = append(fails, fmt.Sprintf("%s is present (%s), want absent", p, canonical(got)))
				} else {
					fails = append(fails, fmt.Sprintf("%s is absent", p))
				}
			}
		case !ok:
			fails = append(fails, fmt.Sprintf("%s is absent", p))
		case m.Regex != nil:
			s, isString := got.(string)
			if !isString {
				s = canonical(got)
			}
			if !m.Regex.MatchString(s) {
				fails = append(fails, fmt.Sprintf("%s is %s, want a match of /%s/", p, canonical(got), m.Regex))
			}
		case m.OneOf != nil:
			hit := false
			for _, o := range m.OneOf {
				if canonical(o) == canonical(got) {
					hit = true
				}
			}
			if !hit {
				fails = append(fails, fmt.Sprintf("%s is %s, want one of %s", p, canonical(got), canonical(m.OneOf)))
			}
		default:
			if canonical(m.Equal) != canonical(got) {
				fails = append(fails, fmt.Sprintf("%s is %s, want %s", p, canonical(got), canonical(m.Equal)))
			}
		}
	}
	return fails
}

var varRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substitute replaces ${name} in a payload: a string that is only "${name}" becomes the
// variable's JSON value (keeping numbers numbers), and ${name} inside a longer string its
// text. Built in: ${now} (UTC, RFC 3339 with milliseconds), ${uuid}, ${identity}.
func substitute(raw json.RawMessage, vars map[string]any, identity string) (json.RawMessage, error) {
	if raw == nil || !varRE.Match(raw) {
		return raw, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var missing []string
	value := func(name string) (any, bool) {
		switch name {
		case "now":
			return time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), true
		case "uuid":
			return uuid(), true
		case "identity":
			return identity, true
		}
		v, ok := vars[name]
		return v, ok
	}
	var walk func(any) any
	walk = func(x any) any {
		switch v := x.(type) {
		case map[string]any:
			for k, e := range v {
				v[k] = walk(e)
			}
			return v
		case []any:
			for i, e := range v {
				v[i] = walk(e)
			}
			return v
		case string:
			if m := varRE.FindStringSubmatch(v); m != nil && m[0] == v {
				if val, ok := value(m[1]); ok {
					return val
				}
				missing = append(missing, m[1])
				return v
			}
			return varRE.ReplaceAllStringFunc(v, func(s string) string {
				name := varRE.FindStringSubmatch(s)[1]
				val, ok := value(name)
				if !ok {
					missing = append(missing, name)
					return s
				}
				if str, isString := val.(string); isString {
					return str
				}
				return canonical(val)
			})
		}
		return x
	}
	doc = walk(doc)
	if len(missing) > 0 {
		return nil, fmt.Errorf("undefined variable ${%s}: save it in an earlier step", missing[0])
	}
	return json.Marshal(doc)
}

func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
