package broker

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

// The owner-only key survives broker restarts. New cursors do not expose a
// database-wide high-water mark or filtered-row positions to the client.
func loadCursorCipher(path string) (cipher.AEAD, error) {
	if err := securefile.CheckParents(path); err != nil {
		return nil, err
	}
	file, err := acquireLock(path)
	if err != nil {
		return nil, fmt.Errorf("open private cursor key: %w", err)
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil {
		return nil, fmt.Errorf("read cursor key")
	}
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate cursor key")
		}
		if _, err := file.Write(key); err != nil {
			return nil, fmt.Errorf("write cursor key")
		}
		if err := file.Sync(); err != nil {
			return nil, fmt.Errorf("sync cursor key")
		}
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return nil, fmt.Errorf("open cursor key directory")
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return nil, fmt.Errorf("sync cursor key directory")
		}
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid cursor key; explicit recovery required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (s *Server) encodeCursor(c cursor) string {
	c.V = 2
	data, _ := json.Marshal(c)
	sealed := s.cursorCipher.Seal(nil, nil, data, []byte(s.cfg.Instance))
	return "c2_" + base64.RawURLEncoding.EncodeToString(sealed)
}

func (s *Server) decodeCursor(value string) (cursor, error) {
	if !strings.HasPrefix(value, "c2_") {
		// Upgrade an existing v1 checkpoint at the same exclusive row, never by
		// deriving a new starting point from history or the current maximum.
		return decodeCursor(value)
	}
	invalid := fmt.Errorf("invalid cursor")
	if len(value) > 2048 {
		return cursor{}, invalid
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "c2_"))
	if err != nil {
		return cursor{}, invalid
	}
	data, err := s.cursorCipher.Open(nil, nil, sealed, []byte(s.cfg.Instance))
	if err != nil {
		return cursor{}, invalid
	}
	var c cursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil || c.V != 2 || c.RowID < 0 || c.ThroughRowID < c.RowID || c.AccountID == "" || c.Generation == "" {
		return cursor{}, invalid
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || c.Complete != (c.RowID == c.ThroughRowID) {
		return cursor{}, invalid
	}
	return c, nil
}
