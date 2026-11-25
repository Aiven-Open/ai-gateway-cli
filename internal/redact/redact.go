// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package redact holds the patterns that recognise credentials in text the user sees, so the configuration preview and
// gateway error messages hide the same names and shapes.
package redact

import "regexp"

// otherWords are the credential words besides token, which a token count also contains.
const otherWords = `key|secret|passw|credential|bearer|auth|cookie|session|header`

// words are the parts of a name that mark it as holding a credential. A header field carries auth headers, as in
// ANTHROPIC_CUSTOM_HEADERS.
const words = `token|` + otherWords

var (
	// tokenCount is a name that sizes something in tokens: a sizing word starts one of its parts and tokens ends it.
	tokenCount = regexp.MustCompile(
		`(?i)(?:^|[_.-])(?:max|min|context|window|input|output|budget)[\w.-]*tokens$`,
	)
	otherWord = regexp.MustCompile(`(?i)` + otherWords)
)

// CountName reports whether name, a field name Name matches, counts tokens rather than holding one. A name that also
// holds another credential word is not a count.
func CountName(name string) bool {
	return tokenCount.MatchString(name) && !otherWord.MatchString(name)
}

// Name matches a field or variable name that may hold a credential: one containing a credential word, or pat as a
// whole part, as in GITHUB_PAT, so that path and pattern do not count. Use it case-insensitively.
const Name = `(?:[\w.-]*(?:` + words + `)[\w.-]*|(?:\b|[\w.-]*[_.-])pat(?:\b|[_.-][\w.-]*))`

// Word matches a credential word in prose, such as a comment. Use it case-insensitively.
const Word = `(?:` + words + `|\bpat\b|_pat\b)`

// Token matches a value shaped like a known credential, whatever holds it: a gateway key in the characters
// creds.ValidKey allows, an sk- key, a GitHub token or an AWS access key ID. It stays case-sensitive inside a
// case-insensitive pattern.
const Token = `(?-i:AVNS_[A-Za-z0-9._-]+|\bsk-[A-Za-z0-9._-]{8,}|\bghp_[A-Za-z0-9]{20,}|` +
	`\bgithub_pat_[A-Za-z0-9_]{20,}|\bAKIA[0-9A-Z]{16}\b)`
