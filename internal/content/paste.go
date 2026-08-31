package content

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Curly quotation marks. Chat assistants produce them when they format an
// answer as prose, and a JSON parser rejects them as delimiters.
const (
	leftDoubleQuote  = '“' // “
	rightDoubleQuote = '”' // ”
)

// repairPaste makes a chat assistant's answer parseable without changing what
// a well-formed file says.
//
// It runs only when the bytes are not already JSON, so a valid seed file is
// returned byte-for-byte untouched and nothing here can corrupt a good paste.
// When the bytes are already broken the worst case is a different parse error,
// which is what the caller was going to report anyway.
func repairPaste(data []byte) []byte {
	if json.Valid(data) {
		return data
	}
	return straightenDelimiters(stripCodeFence(data))
}

// stripCodeFence removes a Markdown fence around the whole payload. Copying an
// answer out of a chat window usually brings the ```json line with it.
func stripCodeFence(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if !bytes.HasPrefix(trimmed, []byte("```")) {
		return data
	}
	// Drop the opening fence and whatever language tag rides on that line.
	if nl := bytes.IndexByte(trimmed, '\n'); nl >= 0 {
		trimmed = trimmed[nl+1:]
	} else {
		return data
	}
	if end := bytes.LastIndex(trimmed, []byte("```")); end >= 0 {
		trimmed = trimmed[:end]
	}
	return bytes.TrimSpace(trimmed)
}

// straightenDelimiters converts the curly double quotes that are acting as
// JSON delimiters into straight ones, and leaves the curly quotes that are
// part of a question's text alone.
//
// Telling those apart is the whole job, and a blind replacement gets it wrong:
// a prompt like `... described as “daft punky thrash,” and later ...` has a
// quotation inside the string, and straightening those two marks ends the
// string early and turns the rest of the question into garbage JSON.
//
// The rule is JSON's own grammar. A string ends where the next thing is
// structural, so a curly quote counts as a closing delimiter only when what
// follows it -- past any whitespace -- is one of , : } ] or the end of input.
// Anything else means the mark is inside the text and is copied through.
//
// Single curly quotes are never touched: JSON has no single-quoted strings, so
// an apostrophe in `Darlin’` or `Roget’s` is only ever content.
func straightenDelimiters(data []byte) []byte {
	runes := []rune(string(data))
	var b strings.Builder
	b.Grow(len(data))

	inString := false
	escaped := false
	for i, r := range runes {
		switch {
		case escaped:
			escaped = false
			b.WriteRune(r)
		case inString && r == '\\':
			escaped = true
			b.WriteRune(r)
		case inString && r == '"':
			inString = false
			b.WriteRune(r)
		case inString && (r == leftDoubleQuote || r == rightDoubleQuote):
			if structuralNext(runes, i+1) {
				inString = false
				b.WriteRune('"')
			} else {
				b.WriteRune(r)
			}
		case !inString && r == '"':
			inString = true
			b.WriteRune(r)
		case !inString && (r == leftDoubleQuote || r == rightDoubleQuote):
			inString = true
			b.WriteRune('"')
		default:
			b.WriteRune(r)
		}
	}
	return []byte(b.String())
}

// structuralNext reports whether the next non-space rune is one that can
// legally follow a JSON string, which is what makes the quote before it a
// closing delimiter rather than part of the text.
func structuralNext(runes []rune, i int) bool {
	for ; i < len(runes); i++ {
		switch runes[i] {
		case ' ', '\t', '\r', '\n':
			continue
		case ',', ':', '}', ']':
			return true
		default:
			return false
		}
	}
	// Nothing but space to the end of the payload: the document is closing.
	return true
}
