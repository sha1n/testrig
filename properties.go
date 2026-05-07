package testrig

// Properties represents dynamic configuration produced by services. It is a
// flat string-to-string map; callers feed it into their config library
// (Viper, koanf, etc.) for any typed parsing.
type Properties map[string]string

// snapshot returns a deep copy of the properties map. Used internally
// whenever a stable, immutable view is required (e.g. hook contexts) to
// prevent aliasing against the live internal map.
func (p Properties) snapshot() Properties {
	cp := make(Properties, len(p))
	for k, v := range p {
		cp[k] = v
	}
	return cp
}
