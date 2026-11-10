// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

// Package creds owns the key files, one per gateway, which the launcher and every tool it configures read, so no
// shell startup file has to export the key. The default gateway's is ~/.config/aiven/ai-gateway-key, the file the Aiven
// Console instructions write; every other gateway's is in ~/.config/aiven/ai-gateway-keys.
package creds

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aiven-open/ai-gateway-cli/internal/config"
	"github.com/aiven-open/ai-gateway-cli/internal/fsutil"
)

const (
	// File is the default gateway's key file under the user's home: the bare key and a newline, mode 0600.
	File = ".config/aiven/ai-gateway-key"
	// Dir holds the key file of every other gateway, named by Name.
	Dir = ".config/aiven/ai-gateway-keys"
	// EnvVar holds a key setup may save. It names no gateway, so nothing reads it in place of a key file.
	EnvVar = "AIVEN_AI_GATEWAY_KEY"
)

var (
	keyCharset = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	notSlug    = regexp.MustCompile(`[^a-z0-9.-]+`)
)

// ValidKey reports whether key consists solely of safe gateway-key characters.
func ValidKey(key string) bool { return keyCharset.MatchString(key) }

// Name is the key file name of a gateway other than the default: a readable slug of its origin, then the first 12
// hex digits of the origin's SHA-256, which tells apart origins with the same slug. It uses only [a-z0-9.-], so it
// needs no quoting in any shell the tools run their key command in, and never ends in a dot, which Windows drops.
func Name(origin string) string {
	slug := strings.Trim(notSlug.ReplaceAllString(origin, "-"), ".-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], ".-")
	}
	sum := sha256.Sum256([]byte(origin))
	return slug + "-" + hex.EncodeToString(sum[:])[:12]
}

// Rel is the key file of the gateway at origin, relative to the user's home and with forward slashes, as the tools'
// key commands name it.
func Rel(origin string) string {
	if origin == config.DefaultBaseURL {
		return File
	}
	return Dir + "/" + Name(origin)
}

// Path is the key file of the gateway at origin under userHome.
func Path(userHome, origin string) string {
	return filepath.Join(userHome, filepath.FromSlash(Rel(origin)))
}

// Helper is a POSIX shell command that prints the key for the gateway at origin, its key file's content, and fails
// without it. It has no EnvVar fallback, as the Aiven Console's helper does: the variable names no gateway, so it would
// send one gateway's key to another. On Windows it is for Git Bash, where the home is USERPROFILE, as config.HomeVar
// has it, rather than a HOME that Git Bash may set elsewhere.
func Helper(origin, goos string) string {
	if goos == "windows" {
		return `cat "$USERPROFILE/` + Rel(origin) + `"`
	}
	return "cat ~/" + Rel(origin)
}

// PowerShell is Helper as a Windows PowerShell script, which also fails without the key file. Every literal in it is
// single-quoted and it holds no $, ", backtick, backslash, ! or %, so it passes unchanged through cmd.exe and a
// bash-like shell inside double quotes, and through a program's MSVC-style argument quoting. It calls .NET rather than
// cmdlets, which Windows PowerShell loads from a module search that depends on the environment, and leaves through
// [Environment]::Exit: Claude Code runs the helper with stdin an open pipe, which keeps powershell.exe from exiting by
// itself. Exit skips PowerShell's output pipeline, so the key goes to Console.Out.
func PowerShell(origin string) string {
	path := `[IO.Path]::Combine([Environment]::GetEnvironmentVariable('USERPROFILE'), '` + Rel(origin) + `')`
	return `if ([IO.File]::Exists(` + path + `)) { [Console]::Out.WriteLine([IO.File]::ReadAllText(` + path +
		`).Trim()); [Environment]::Exit(0) } else { [Environment]::Exit(1) }`
}

// Command is the program and arguments a tool runs, without a shell, to print the key for the gateway at origin:
// Helper through sh, or on Windows PowerShell through powershell.exe.
func Command(origin, goos string) (string, []string) {
	if goos == "windows" {
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", PowerShell(origin)}
	}
	return "sh", []string{"-c", Helper(origin, goos)}
}

// CommandLine is Command as one shell command line, for a tool that runs its key command through a shell.
func CommandLine(origin, goos string) string {
	name, args := Command(origin, goos)
	last := len(args) - 1
	if goos == "windows" {
		return name + " " + strings.Join(args[:last], " ") + ` "` + args[last] + `"`
	}
	return name + " " + strings.Join(args[:last], " ") + " '" + args[last] + "'"
}

// Write saves key to the key file of the gateway at origin with write, which creates missing parent directories
// 0700, and returns the file's path.
func Write(userHome, origin, key string, write func(path string, data []byte, perm os.FileMode) error) (string, error) {
	if userHome == "" {
		return "", config.ErrNoHome
	}
	if !ValidKey(key) {
		return "", errors.New("key may contain only letters, digits, '.', '_' and '-'")
	}
	path := Path(userHome, origin)
	return path, write(path, []byte(key+"\n"), 0o600)
}

// Load returns the key for the gateway at origin the way Helper finds it, from its key file, or "" when there is none;
// an invalid key counts as none.
func Load(userHome, origin string) (string, error) {
	if userHome == "" {
		return "", config.ErrNoHome
	}
	data, err := fsutil.ReadFile(Path(userHome, origin))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("read the key file: %w", err)
	}
	return Parse(data), nil
}

// FromEnv returns the last EnvVar value in environ, or "" when there is none or it is invalid.
func FromEnv(environ []string) string {
	var key string
	for _, kv := range environ {
		if name, v, ok := strings.Cut(kv, "="); ok && config.SameVar(name, EnvVar) {
			key = v
		}
	}
	if !ValidKey(key) {
		return ""
	}
	return key
}

// Parse returns the key in a key file's content, or "" when it holds no valid key.
func Parse(data []byte) string {
	if key := strings.TrimSpace(string(data)); ValidKey(key) {
		return key
	}
	return ""
}

// Delete removes the key file of the gateway at origin with remove; an absent one is not an error.
func Delete(userHome, origin string, remove func(path string) error) error {
	if userHome == "" {
		return nil
	}
	if err := remove(Path(userHome, origin)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
