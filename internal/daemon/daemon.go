// Package daemon implements the handoff delivery daemon: it polls each role's
// outbox, delivers messages into recipients' inboxes (in their worktrees),
// wakes the recipients via tmux, and archives sent/failed messages. It ports
// handoffd.bb and stop_handoff_daemon.bb.
package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dudehook/swarmforge/internal/handoff"
)

const (
	pollInterval = time.Second
	wakeMessage  = "You have new handoff mail. If idle, run ready_for_next.sh."
)

// preferredHeaderOrder is the leading header order used when re-rendering a
// delivered message; any other headers follow, sorted.
var preferredHeaderOrder = []string{
	"id", "from", "to", "recipient", "priority", "type", "role", "commit",
	"message", "created_at", "enqueued_at", "dequeued_at", "completed_at",
}

// Daemon delivers handoffs for one project root.
type Daemon struct {
	root       string
	daemonDir  string
	stateDir   string
	rolesFile  string
	socketFile string
	pidFile    string
	stopFile   string
	stopping   atomic.Bool
}

type roleInfo struct {
	role         string
	worktreeName string
	worktreePath string
	session      string
	display      string
	agent        string
	receiveMode  string
}

// New builds a daemon for the given project root.
func New(root string) *Daemon {
	stateDir := filepath.Join(root, ".swarmforge")
	daemonDir := filepath.Join(stateDir, "daemon")
	return &Daemon{
		root:       root,
		stateDir:   stateDir,
		daemonDir:  daemonDir,
		rolesFile:  filepath.Join(stateDir, "roles.tsv"),
		socketFile: filepath.Join(stateDir, "tmux-socket"),
		pidFile:    filepath.Join(daemonDir, "handoffd.pid"),
		stopFile:   filepath.Join(daemonDir, "stop"),
	}
}

// Run starts the poll loop. It writes a pid file, handles SIGTERM/SIGINT by
// requesting a clean stop, and removes the pid file on exit.
func (d *Daemon) Run() error {
	if err := os.MkdirAll(d.daemonDir, 0o755); err != nil {
		return err
	}
	os.Remove(d.stopFile)
	if err := os.WriteFile(d.pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		return err
	}
	defer os.Remove(d.pidFile)

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigc
		d.stopping.Store(true)
	}()

	d.log("started")
	defer d.log("stopped")
	for !d.shouldStop() {
		if d.stateGone() {
			d.log("state directory removed; stopping")
			break
		}
		d.pollOnce()
		d.sleepPoll(pollInterval)
	}
	return nil
}

func (d *Daemon) shouldStop() bool {
	return d.stopping.Load() || fileExists(d.stopFile)
}

// stateGone reports whether the swarm's .swarmforge state directory has
// disappeared (e.g. the project was deleted or reset). When it has, the daemon
// can do nothing useful, so it exits — which also releases any sleep inhibitor
// that was wrapping it.
func (d *Daemon) stateGone() bool {
	_, err := os.Stat(d.stateDir)
	return err != nil
}

func (d *Daemon) sleepPoll(total time.Duration) {
	for remaining := total; remaining > 0 && !d.shouldStop(); {
		step := 100 * time.Millisecond
		if remaining < step {
			step = remaining
		}
		time.Sleep(step)
		remaining -= step
	}
}

func (d *Daemon) pollOnce() {
	if d.shouldStop() {
		return
	}
	roles := d.loadRoles()
	socketBytes, err := os.ReadFile(d.socketFile)
	if err != nil {
		d.log("error", "read socket", err.Error())
		return
	}
	socket := strings.TrimSpace(string(socketBytes))
	for _, role := range sortedRoleKeys(roles) {
		info := roles[role]
		for _, path := range outboxFiles(info) {
			if d.shouldStop() {
				return
			}
			if err := d.deliver(roles, socket, role, path); err != nil {
				d.log("error", path, err.Error())
				if ferr := d.fail(path, err.Error()); ferr != nil {
					d.log("failed-to-archive", path, ferr.Error())
				}
			}
		}
	}
}

func (d *Daemon) loadRoles() map[string]roleInfo {
	roles := map[string]roleInfo{}
	data, err := os.ReadFile(d.rolesFile)
	if err != nil {
		return roles
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		info := roleInfo{}
		info.role = field(f, 0)
		info.worktreeName = field(f, 1)
		info.worktreePath = field(f, 2)
		info.session = field(f, 3)
		info.display = field(f, 4)
		info.agent = field(f, 5)
		info.receiveMode = field(f, 6)
		if info.receiveMode == "" {
			info.receiveMode = "task"
		}
		roles[info.role] = info
	}
	return roles
}

// deliver writes the message into each recipient's inbox/new, wakes them, then
// moves the source into the sender's sent directory.
func (d *Daemon) deliver(roles map[string]roleInfo, socket, senderRole, path string) error {
	filename := filepath.Base(path)
	headers, body, err := parseMessageFile(path)
	if err != nil {
		return err
	}
	recipients := splitNonEmpty(headers["to"], ",")
	if len(recipients) == 0 {
		return d.fail(path, "missing to header")
	}
	for _, recipient := range recipients {
		info, ok := roles[recipient]
		if !ok {
			return fmt.Errorf("unknown recipient %s", recipient)
		}
		target := filepath.Join(info.worktreePath, ".swarmforge", "handoffs", "inbox", "new", filename)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if !fileExists(target) {
			delivered := cloneHeaders(headers)
			delivered["recipient"] = recipient
			delivered["enqueued_at"] = handoff.Timestamp()
			if err := os.WriteFile(target, []byte(renderMessage(delivered, body)), 0o644); err != nil {
				return err
			}
		}
		if err := notify(socket, info.session); err != nil {
			return err
		}
	}
	sentDir := filepath.Join(roles[senderRole].worktreePath, ".swarmforge", "handoffs", "sent")
	if err := moveWithCollision(path, sentDir); err != nil {
		return err
	}
	d.log("delivered", path)
	return nil
}

func (d *Daemon) fail(path, reason string) error {
	failedDir := filepath.Join(filepath.Dir(filepath.Dir(path)), "failed")
	d.log("failed", path, reason)
	os.WriteFile(path+".error", []byte(reason+"\n"), 0o644)
	return moveWithCollision(path, failedDir)
}

func (d *Daemon) log(parts ...string) {
	os.MkdirAll(d.daemonDir, 0o755)
	f, err := os.OpenFile(filepath.Join(d.daemonDir, "handoffd.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", handoff.Timestamp(), strings.Join(parts, " "))
}

// parseMessageFile splits a message file into headers and body.
func parseMessageFile(path string) (map[string]string, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	headers, body := parseMessage(string(data))
	return headers, body, nil
}

func parseMessage(content string) (map[string]string, string) {
	headerBlock := content
	body := ""
	if idx := strings.Index(content, "\n\n"); idx >= 0 {
		headerBlock = content[:idx]
		body = content[idx+2:]
	}
	headers := map[string]string{}
	for _, line := range strings.Split(headerBlock, "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			headers[k] = v
		}
	}
	return headers, body
}

func renderMessage(headers map[string]string, body string) string {
	seen := map[string]bool{}
	var lines []string
	for _, k := range preferredHeaderOrder {
		if v, ok := headers[k]; ok {
			lines = append(lines, k+": "+v)
			seen[k] = true
		}
	}
	var remaining []string
	for k := range headers {
		if !seen[k] {
			remaining = append(remaining, k)
		}
	}
	sort.Strings(remaining)
	for _, k := range remaining {
		lines = append(lines, k+": "+headers[k])
	}
	return strings.Join(lines, "\n") + "\n\n" + body
}

// notify wakes the recipient agent by typing the wake message and pressing
// Enter (carriage return then line feed) in its tmux session.
func notify(socket, session string) error {
	if err := tmux(socket, "send-keys", "-t", session, "-l", wakeMessage); err != nil {
		return fmt.Errorf("tmux send text failed: %w", err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := tmux(socket, "send-keys", "-t", session, "C-m"); err != nil {
		return fmt.Errorf("tmux send carriage return failed: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := tmux(socket, "send-keys", "-t", session, "C-j"); err != nil {
		return fmt.Errorf("tmux send line feed failed: %w", err)
	}
	return nil
}

func tmux(socket string, args ...string) error {
	return exec.Command("tmux", append([]string{"-S", socket}, args...)...).Run()
}

func outboxFiles(info roleInfo) []string {
	outbox := filepath.Join(info.worktreePath, ".swarmforge", "handoffs", "outbox")
	entries, err := os.ReadDir(outbox)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".handoff") {
			out = append(out, filepath.Join(outbox, e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool { return filepath.Base(out[i]) < filepath.Base(out[j]) })
	return out
}

// moveWithCollision moves source into targetDir, prefixing the name with a
// timestamp if a file of the same name already exists.
func moveWithCollision(source, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}
	base := filepath.Base(source)
	target := filepath.Join(targetDir, base)
	if fileExists(target) {
		target = filepath.Join(targetDir, handoff.Timestamp()+"_"+base)
	}
	return os.Rename(source, target)
}

// Stop signals a running daemon to stop: it writes the stop file, sends SIGTERM
// to the pid (escalating to SIGKILL after a timeout), and clears the pid/stop
// files. Ports stop_handoff_daemon.bb.
func Stop(root string) error {
	daemonDir := filepath.Join(root, ".swarmforge", "daemon")
	pidFile := filepath.Join(daemonDir, "handoffd.pid")
	stopFile := filepath.Join(daemonDir, "stop")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		return err
	}
	if !fileExists(stopFile) {
		os.WriteFile(stopFile, []byte(""), 0o644)
	}
	if data, err := os.ReadFile(pidFile); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil {
			if processAlive(pid) {
				syscall.Kill(pid, syscall.SIGTERM)
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) && processAlive(pid) {
					time.Sleep(100 * time.Millisecond)
				}
				if processAlive(pid) {
					syscall.Kill(pid, syscall.SIGKILL)
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
		os.Remove(pidFile)
	}
	os.Remove(stopFile)
	return nil
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func field(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}

func sortedRoleKeys(roles map[string]roleInfo) []string {
	keys := make([]string, 0, len(roles))
	for k := range roles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitNonEmpty(s, sep string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h)+2)
	for k, v := range h {
		out[k] = v
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
