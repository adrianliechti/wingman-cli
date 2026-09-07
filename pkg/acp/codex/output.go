package codex

import (
	"bytes"
	"unicode/utf8"
)

const maxToolOutputBytes = 1024 * 1024

type toolOutput struct {
	data      bytes.Buffer
	truncated bool
}

func (b *toolOutput) append(text string) {
	if len(text) > maxToolOutputBytes {
		text = text[len(text)-maxToolOutputBytes:]
		b.data.Reset()
		b.truncated = true
	}
	if excess := b.data.Len() + len(text) - maxToolOutputBytes; excess > 0 {
		b.data.Next(excess)
		b.truncated = true
	}
	b.data.WriteString(text)
}

func (b *toolOutput) String() string {
	data := b.data.Bytes()
	if !b.truncated {
		return string(data)
	}
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return "[Output truncated; showing the last 1 MiB.]\n" + string(data)
}

func boundedToolOutput(text string) string {
	var b toolOutput
	b.append(text)
	return b.String()
}
