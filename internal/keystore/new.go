// Copyright (c) 2026 Aiven, Helsinki, Finland. https://aiven.io/

package keystore

import (
	"fmt"
	"io"
)

// New builds the backend for an AIVEN_LLM_KEYSTORE mode; notice receives the file-fallback message.
func New(mode, home string, notice io.Writer) (Store, error) {
	switch mode {
	case "file":
		return NewFile(home), nil
	case "keyring":
		return NewKeyring(), nil
	case "auto":
		return &Chain{Primary: NewKeyring(), Fallback: NewFile(home), Notice: notice}, nil
	}
	return nil, fmt.Errorf("invalid AIVEN_LLM_KEYSTORE %q: use file, keyring or auto", mode)
}
