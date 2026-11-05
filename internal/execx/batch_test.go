// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package execx

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestIsBatchRecognisesCmdAndBatCaseInsensitively(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		`C:\npm\claude.cmd`: true,
		`C:\npm\PI.CMD`:     true,
		`C:\npm\tool.bat`:   true,
		`C:\npm\TOOL.BAT`:   true,
		`C:\npm\codex.exe`:  false,
		`/usr/bin/claude`:   false,
	} {
		if got := isBatch(path); got != want {
			t.Errorf("isBatch(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestBatchCmdLinePreservesMetacharactersQuotesAndEnvSyntaxAcrossCmdAndArgvParsers(t *testing.T) {
	t.Parallel()
	args := []string{
		"--flag",
		"two words",
		"",
		`quote"slash\`,
		"$HOME; & | * < > ( ) ^",
		"%USERPROFILE%",
		"100% & %PATH% %A_B%",
		`{"apiKeyHelper":"sh -c '[ -f ~/.config/aiven/ai-gateway-key ] && cat ~/.config/aiven/ai-gateway-key || printf %s \"$AIVEN_AI_GATEWAY_KEY\"'","env":{"ANTHROPIC_BASE_URL":"https://ai.aiven.io"}}`,
		`{"apiKeyHelper":"powershell.exe -NoProfile -NonInteractive -Command \"if (Test-Path 'x') { exit 0 } else { exit 1 }\""}`,
		`"`,
		`"""`,
		`\"\"`,
		`C:\Users\Jane Doe\trailing\\`,
		"日本語",
	}
	line, err := BatchCmdLine(`C:\Windows\System32\cmd.exe`, `C:\Users\Jane Doe\AppData\Roaming\npm\claude.cmd`, args)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `"C:\Windows\System32\cmd.exe" /d /v:off /s /c "`
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `"`) {
		t.Fatalf("line = %s", line)
	}
	// cmd.exe /d /v:off /s /c strips the first and last quote after /c, then runs two cmd.exe passes (/c and %* in the shim).
	inner := line[len(prefix) : len(line)-1]
	if strings.Contains(inner, "%USERPROFILE%") || strings.Contains(inner, "%PATH%") || strings.Contains(inner, "%A_B%") {
		t.Errorf("unbroken %%VAR%% left in command line: %s", inner)
	}
	pass1, err := simulateCmdPass(inner)
	if err != nil {
		t.Fatalf("pass 1 (/c): %v\ninner: %s", err, inner)
	}
	pass2, err := simulateCmdPass(pass1)
	if err != nil {
		t.Fatalf("pass 2 (%%*): %v\npass1: %s", err, pass1)
	}
	if got := parseWinArgv(pass2)[1:]; !slices.Equal(got, args) {
		t.Errorf("CommandLineToArgvW decoded %q, want %q", got, args)
	}
	if got := parseUCRTArgv(pass2)[1:]; !slices.Equal(got, args) {
		t.Errorf("UCRT decoded %q, want %q", got, args)
	}
}

func TestBatchCmdLineRefusesUnescapableInputWithoutExposingArgv(t *testing.T) {
	t.Parallel()
	const secret = "AVNS_secret_token"
	for _, args := range [][]string{
		{secret + "\nnext"},
		{secret + "\rnext"},
	} {
		_, err := BatchCmdLine(`C:\Windows\System32\cmd.exe`, `C:\npm\pi.cmd`, args)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("BatchCmdLine(%q) = %v, want secret-safe refusal", args, err)
		}
	}
	for _, path := range []string{"", `C:\bad"path\pi.cmd`, `C:\bad%path\pi.cmd`, "C:\\bad\npath\\pi.cmd"} {
		if _, err := BatchCmdLine(`C:\Windows\System32\cmd.exe`, path, nil); err == nil {
			t.Errorf("BatchCmdLine path %q succeeded", path)
		}
	}
}

func simulateCmdPass(line string) (string, error) {
	var out strings.Builder
	inQuotes := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuotes {
			if c == '"' {
				inQuotes = false
			}
			out.WriteByte(c)
			continue
		}
		switch c {
		case '^':
			i++
			if i < len(line) {
				out.WriteByte(line[i])
			}
		case '"':
			inQuotes = true
			out.WriteByte(c)
		case '&', '|', '<', '>', '(', ')':
			return "", errors.New("unquoted cmd.exe metacharacter: " + string(c))
		default:
			out.WriteByte(c)
		}
	}
	if inQuotes {
		return "", errors.New("unclosed quote in cmd.exe pass")
	}
	return out.String(), nil
}

// parseWinArgv mirrors Go's commandLineToArgv (src/os/exec_windows.go) and Windows CommandLineToArgvW.
func parseWinArgv(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var b []byte
		inquote := false
		nslash := 0
		for ; len(cmd) > 0; cmd = cmd[1:] {
			c := cmd[0]
			if (c == ' ' || c == '\t') && !inquote {
				b = append(b, strings.Repeat(`\`, nslash)...)
				nslash = 0
				cmd = cmd[1:]
				break
			}
			if c == '"' {
				b = append(b, strings.Repeat(`\`, nslash/2)...)
				if nslash%2 == 0 {
					if inquote && len(cmd) > 1 && cmd[1] == '"' {
						b = append(b, c)
						cmd = cmd[1:]
					}
					inquote = !inquote
				} else {
					b = append(b, c)
				}
				nslash = 0
				continue
			}
			if c == '\\' {
				nslash++
				continue
			}
			b = append(b, strings.Repeat(`\`, nslash)...)
			nslash = 0
			b = append(b, c)
		}
		b = append(b, strings.Repeat(`\`, nslash)...)
		args = append(args, string(b))
	}
	return args
}

// parseUCRTArgv mirrors Microsoft's UCRT parse_command_line (used by node.exe), where "" inside quotes keeps quote mode.
func parseUCRTArgv(cmd string) []string {
	var args []string
	for i := 0; i < len(cmd); {
		for i < len(cmd) && (cmd[i] == ' ' || cmd[i] == '\t') {
			i++
		}
		if i >= len(cmd) {
			break
		}
		var b []byte
		inquote := false
		for i < len(cmd) {
			copyChar := true
			numSlash := 0
			for i < len(cmd) && cmd[i] == '\\' {
				i++
				numSlash++
			}
			if i < len(cmd) && cmd[i] == '"' {
				if numSlash%2 == 0 {
					if inquote && i+1 < len(cmd) && cmd[i+1] == '"' {
						i++
					} else {
						copyChar = false
						inquote = !inquote
					}
				}
				numSlash /= 2
			}
			b = append(b, strings.Repeat(`\`, numSlash)...)
			if i >= len(cmd) || (!inquote && (cmd[i] == ' ' || cmd[i] == '\t')) {
				break
			}
			if copyChar {
				b = append(b, cmd[i])
			}
			i++
		}
		args = append(args, string(b))
	}
	return args
}
