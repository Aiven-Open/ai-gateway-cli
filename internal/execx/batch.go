// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx

import (
	"errors"
	"path/filepath"
	"strings"
)

// isBatch reports whether path names a Windows batch file, which CreateProcess runs through cmd.exe.
func isBatch(path string) bool {
	ext := filepath.Ext(path)
	return strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".bat")
}

// BatchCmdLine formats a cmd.exe /d /v:off /s /c command line that runs the batch file at path with args. /v:off
// holds for both passes whatever the DelayedExpansion registry value says, which would otherwise expand !VAR! and
// drop a lone ! or another ^ from the arguments.
//
// An npm shim on Windows is a .cmd file ending in `"%_prog%" "%dp0%\..." %*`, so each argument passes through cmd.exe
// twice (once on the /c line and once when %* expands) before the target binary's CommandLineToArgvW or C runtime
// splits it. Wrapping each quote-free span in "..." keeps cmd.exe in quote mode across spaces and metacharacters
// (&, |, <, >, (, ), ^, ;, ,, =); closing quotes around each literal " and writing \^^^" lets both cmd.exe passes
// consume one ^ and leave \" outside cmd.exe quotes for the target binary. Splitting after % when followed by an
// identifier character keeps cmd.exe from expanding %VAR% on the /c line.
func BatchCmdLine(comspec, path string, args []string) (string, error) {
	if comspec == "" || path == "" || strings.ContainsAny(comspec, "\r\n\x00\"") || strings.ContainsAny(path, "\r\n\x00\"%") {
		return "", errors.New("cannot quote batch interpreter or script path")
	}
	var inner strings.Builder
	inner.WriteByte('"')
	inner.WriteString(path)
	inner.WriteByte('"')
	for _, arg := range args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return "", errors.New("cannot pass an argument containing a newline or NUL to a Windows batch file")
		}
		inner.WriteByte(' ')
		writeBatchArg(&inner, arg)
	}
	return `"` + comspec + `" /d /v:off /s /c "` + inner.String() + `"`, nil
}

func writeBatchArg(b *strings.Builder, s string) {
	if s == "" {
		b.WriteString(`""`)
		return
	}
	for i, part := range strings.Split(s, `"`) {
		if i > 0 {
			b.WriteString(`\^^^"`)
		}
		writeBatchSpan(b, part)
	}
}

func writeBatchSpan(b *strings.Builder, s string) {
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+1 < len(s) && isEnvChar(s[i+1]) {
			writeQuotedChunk(b, s[start:i+1])
			b.WriteByte(s[i+1])
			i++
			start = i + 1
		}
	}
	writeQuotedChunk(b, s[start:])
}

func writeQuotedChunk(b *strings.Builder, s string) {
	if s == "" {
		return
	}
	b.WriteByte('"')
	b.WriteString(s)
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		b.WriteByte('\\')
	}
	b.WriteByte('"')
}

func isEnvChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}
