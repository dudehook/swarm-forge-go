// Package handoff implements the on-disk handoff message format: header
// parsing and editing, payload extraction, message/batch listing, the
// mkdir-locked sequence counter, and the TASK/BATCH print helpers.
//
// It ports handoff_lib.bb and the duplicated helpers in the ready_for_next*.bb
// and done_with_current*.bb scripts.
package handoff

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ExitError carries a process exit status and a message to print to stderr.
// It mirrors the (System/exit status) + stderr behavior of the Babashka scripts.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string { return e.Message }

// Errorf builds an ExitError with a formatted message.
func Errorf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// StateDir is .swarmforge/handoffs under the working directory.
func StateDir(workDir string) string {
	return filepath.Join(workDir, ".swarmforge", "handoffs")
}

// InboxDir is the inbox under StateDir.
func InboxDir(workDir string) string {
	return filepath.Join(StateDir(workDir), "inbox")
}

// Timestamp returns an ISO-8601 instant in UTC (matches ISO_INSTANT usage).
func Timestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// IDTimestamp returns a compact UTC timestamp for message and batch ids.
func IDTimestamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

var seqRe = regexp.MustCompile(`^[0-9]+$`)

// SplitLines splits on \n or \r\n and drops trailing empty lines, matching
// clojure.string/split-lines.
func SplitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// HeaderField returns the value of the named header from content, searching
// only the header block (lines up to the first blank line).
func HeaderField(content, field string) (string, bool) {
	prefix := field + ": "
	for _, line := range SplitLines(content) {
		if line == "" {
			break
		}
		if strings.HasPrefix(line, prefix) {
			return line[len(prefix):], true
		}
	}
	return "", false
}

// FileHeader reads file and returns the named header value.
func FileHeader(file, field string) (string, bool, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", false, err
	}
	v, ok := HeaderField(string(data), field)
	return v, ok, nil
}

func fileHeaderOr(file, field, def string) (string, error) {
	v, ok, err := FileHeader(file, field)
	if err != nil {
		return "", err
	}
	if !ok {
		return def, nil
	}
	return v, nil
}

// Body returns the payload of file: everything after the first blank-line
// separator, or "" if there is none.
func Body(file string) (string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	s := string(data)
	if idx := strings.Index(s, "\n\n"); idx >= 0 {
		return s[idx+2:], nil
	}
	return "", nil
}

// SetHeader writes field: value into file's header block, replacing an existing
// occurrence or inserting before the blank separator, then atomically replaces
// the file. It reproduces the loop in the original set-header! helpers.
func SetHeader(file, field, value string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	lines := SplitLines(string(data))
	prefix := field + ": "
	out := make([]string, 0, len(lines)+1)
	inserted := false
	replaced := false
	for _, line := range lines {
		switch {
		case !inserted && line == "":
			if !replaced {
				out = append(out, prefix+value)
			}
			out = append(out, line)
			inserted = true
		case !inserted && strings.HasPrefix(line, prefix):
			out = append(out, prefix+value)
			replaced = true
		default:
			out = append(out, line)
		}
	}
	if !inserted && !replaced {
		out = append(out, prefix+value)
	}
	content := strings.Join(out, "\n") + "\n"
	return writeFileAtomic(file, content)
}

// writeFileAtomic writes content to a temp file in the destination directory
// then renames it over file.
func writeFileAtomic(file, content string) error {
	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, ".headers.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, file)
}

// Files returns the sorted .handoff files directly in dir.
func Files(dir string) ([]string, error) {
	return listSorted(dir, func(e os.DirEntry) bool {
		return e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".handoff")
	})
}

// BatchDirs returns the sorted batch_ subdirectories directly in dir.
func BatchDirs(dir string) ([]string, error) {
	return listSorted(dir, func(e os.DirEntry) bool {
		return e.IsDir() && strings.HasPrefix(e.Name(), "batch_")
	})
}

func listSorted(dir string, keep func(os.DirEntry) bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if keep(e) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return filepath.Base(out[i]) < filepath.Base(out[j])
	})
	return out, nil
}

// PrintTask writes the TASK block for file.
func PrintTask(out io.Writer, file string) error {
	taskName, hasTask, err := FileHeader(file, "task")
	if err != nil {
		return err
	}
	from, err := fileHeaderOr(file, "from", "unknown")
	if err != nil {
		return err
	}
	typ, err := fileHeaderOr(file, "type", "unknown")
	if err != nil {
		return err
	}
	priority, err := fileHeaderOr(file, "priority", "50")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "TASK: %s\n", file)
	fmt.Fprintf(out, "FROM: %s\n", from)
	fmt.Fprintf(out, "TYPE: %s\n", typ)
	fmt.Fprintf(out, "PRIORITY: %s\n", priority)
	if hasTask {
		fmt.Fprintf(out, "TASK_NAME: %s\n", taskName)
	}
	fmt.Fprintln(out, "PAYLOAD:")
	body, err := Body(file)
	if err != nil {
		return err
	}
	fmt.Fprint(out, body)
	return nil
}

// PrintBatch writes the BATCH block for batchDir.
func PrintBatch(out io.Writer, batchDir string) error {
	files, err := Files(batchDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return Errorf(2, "AMBIGUOUS_TASK_STATE: batch contains no tasks: %s", batchDir)
	}
	priority, err := fileHeaderOr(files[0], "priority", "50")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "BATCH: %s\n", batchDir)
	fmt.Fprintf(out, "COUNT: %d\n", len(files))
	fmt.Fprintf(out, "PRIORITY: %s\n", priority)
	for i, file := range files {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "BATCH_ITEM: %d\n", i+1)
		if err := PrintTask(out, file); err != nil {
			return err
		}
	}
	return nil
}

// NextSequence allocates the next zero-padded sequence number, guarding the
// counter file with a mkdir-based lock that spins until acquired.
func NextSequence(workDir string) (string, error) {
	dir := StateDir(workDir)
	seqFile := filepath.Join(dir, "sequence")
	lockDir := filepath.Join(dir, "sequence.lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			break
		}
		if errors.Is(err, fs.ErrExist) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return "", err
	}
	defer os.Remove(lockDir)

	last := 0
	if data, err := os.ReadFile(seqFile); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if seqRe.MatchString(trimmed) {
			if n, err := strconv.Atoi(trimmed); err == nil {
				last = n
			}
		}
	}
	next := last + 1
	formatted := fmt.Sprintf("%06d", next)
	if err := os.WriteFile(seqFile, []byte(formatted+"\n"), 0o644); err != nil {
		return "", err
	}
	return formatted, nil
}

// Bullets renders items as a "- item" list joined by newlines, matching the
// list formatting used in the scripts' error messages.
func Bullets(items []string) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = "- " + it
	}
	return strings.Join(parts, "\n")
}
