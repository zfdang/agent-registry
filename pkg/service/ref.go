package service

import "strings"

// IsRegistryID reports whether ref is a registry-internal agent id
// (agt_<uuid>).
func IsRegistryID(ref string) bool {
	return strings.HasPrefix(ref, "agt_")
}

// IsURLShaped reports whether ref should be tried as a URL first — it
// contains a "." or "/" (04). This also catches DID-style agent ids
// (e.g. "did:example:acme.io"), which the resolver falls back to trying as
// a plain agent_id if the URL form doesn't resolve.
func IsURLShaped(ref string) bool {
	return strings.ContainsAny(ref, "./")
}

// PrepareURLRef prepends "https://" to a scheme-less ref so it can be
// normalized and matched against stored URLs (04: "the scheme is dropped
// in references").
func PrepareURLRef(ref string) string {
	if strings.Contains(ref, "://") {
		return ref
	}
	return "https://" + ref
}
