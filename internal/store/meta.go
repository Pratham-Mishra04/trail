package store

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bytedance/sonic"

	"github.com/Pratham-Mishra04/trail/internal/logentry"
)

// writeMeta encodes m as a single JSON line followed by '\n' and writes it to w.
// If m.Type is empty, it's set to logentry.TypeMeta first.
func writeMeta(w io.Writer, m logentry.MetaHeader) error {
	if m.Type == "" {
		m.Type = logentry.TypeMeta
	}
	b, err := sonic.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

// readMeta reads the first line of r and decodes it as a MetaHeader. Returns
// an error if the line is missing, malformed, or not type=="meta".
func readMeta(r io.Reader) (logentry.MetaHeader, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return logentry.MetaHeader{}, fmt.Errorf("read meta line: %w", err)
	}
	if len(line) == 0 {
		return logentry.MetaHeader{}, errors.New("empty session file: no meta header")
	}
	if line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	var m logentry.MetaHeader
	if err := sonic.Unmarshal(line, &m); err != nil {
		return logentry.MetaHeader{}, fmt.Errorf("unmarshal meta: %w", err)
	}
	if m.Type != logentry.TypeMeta {
		return logentry.MetaHeader{}, fmt.Errorf("first line is not a meta header (type=%q)", m.Type)
	}
	return m, nil
}

// sessionsDir resolves $HOME/.config/trail/sessions, creating it if needed.
// Errors out if os.UserHomeDir() fails — no fallback path.
func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w (set $HOME or run trail in an environment that exposes a home directory)", err)
	}
	dir := filepath.Join(home, ".config", "trail", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sessions directory %s: %w", dir, err)
	}
	return dir, nil
}
