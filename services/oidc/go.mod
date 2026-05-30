module github.com/sha1n/testrig/services/oidc

go 1.25.0

require (
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/sha1n/testrig v0.0.0-alpha.2
)

// Local-development override: lets the workspace build before any engine tag
// has been published. Replaces are ignored by external consumers, so this
// only affects builds within this repo.
replace github.com/sha1n/testrig => ../..
