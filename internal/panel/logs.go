package panel

import (
	"bytes"
	"io"
	"os"
	"strings"
)

const (
	maxLogLines   = 2000
	maxLogTailRaw = 2 << 20 // read at most the last 2 MiB of the log
)

// tailLines returns the last n lines of path that contain q (case-insensitive),
// and whether older lines were cut off.
func tailLines(path string, n int, q string) ([]string, bool, error) {
	if n <= 0 || n > maxLogLines {
		n = 500
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []string{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	start := int64(0)
	if st.Size() > maxLogTailRaw {
		start = st.Size() - maxLogTailRaw
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	if start > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	q = strings.ToLower(q)
	lines := []string{}
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if l == "" || (q != "" && !strings.Contains(strings.ToLower(l), q)) {
			continue
		}
		lines = append(lines, l)
	}
	truncated := start > 0
	if len(lines) > n {
		lines, truncated = lines[len(lines)-n:], true
	}
	return lines, truncated, nil
}
