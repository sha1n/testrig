package testrig

import (
	"fmt"
	"strconv"
	"time"
)

// Properties represents dynamic configuration produced by services.
type Properties map[string]string

// Int returns the value for the given key as an int.
func (p Properties) Int(key string) (int, error) {
	val, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("property %s not found", key)
	}
	return strconv.Atoi(val)
}

// Bool returns the value for the given key as a bool.
func (p Properties) Bool(key string) (bool, error) {
	val, ok := p[key]
	if !ok {
		return false, fmt.Errorf("property %s not found", key)
	}
	return strconv.ParseBool(val)
}

// Duration returns the value for the given key as a time.Duration.
func (p Properties) Duration(key string) (time.Duration, error) {
	val, ok := p[key]
	if !ok {
		return 0, fmt.Errorf("property %s not found", key)
	}
	return time.ParseDuration(val)
}

// snapshot returns a deep copy of the properties map.
// Use this whenever a stable, immutable view is required (e.g. hook contexts)
// to prevent aliasing against the live internal map.
func (p Properties) snapshot() Properties {
	cp := make(Properties, len(p))
	for k, v := range p {
		cp[k] = v
	}
	return cp
}
