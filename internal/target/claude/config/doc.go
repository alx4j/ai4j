// Package config resolves Claude's documented per-user configuration root
// without opening mutation authority or interpreting Claude-private schemas.
//
// The documented default is ~/.claude. CLAUDE_CONFIG_DIR replaces that root,
// but an override candidate is accepted only when an exact Claude version
// policy permits it and it maps canonically beneath the trusted current-user
// home. This package currently emits only explicitly unqualified candidates.
// A separately reviewed target-neutral host proof must establish presence,
// ownership, containment, and no-follow identity before activation. Discovery
// never creates a missing directory.
package config
