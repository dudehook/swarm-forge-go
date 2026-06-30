// Package inbox implements the per-role inbox state machine: accepting the next
// task or batch from inbox/new into inbox/in_process, resuming work already in
// process, and completing current work into inbox/completed.
//
// It ports ready_for_next.bb, ready_for_next_task.bb, ready_for_next_batch.bb,
// done_with_current.bb, done_with_current_task.bb, and done_with_current_batch.bb.
package inbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dudehook/swarmforge/internal/handoff"
	"github.com/dudehook/swarmforge/internal/project"
)

type inboxPaths struct {
	new       string
	inProcess string
	completed string
}

func paths(workDir string) inboxPaths {
	inbox := handoff.InboxDir(workDir)
	return inboxPaths{
		new:       filepath.Join(inbox, "new"),
		inProcess: filepath.Join(inbox, "in_process"),
		completed: filepath.Join(inbox, "completed"),
	}
}

func ensureDirs(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ReadyForNext dispatches to the task or batch flow based on the role's
// configured receive mode.
func ReadyForNext(out io.Writer, workDir, root, role string) error {
	mode, err := project.ReceiveMode(root, role)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	switch mode {
	case "batch":
		return ReadyForNextBatch(out, workDir)
	case "task":
		return ReadyForNextTask(out, workDir)
	default:
		return handoff.Errorf(2, "INVALID_RECEIVE_MODE: %s for role %s", mode, role)
	}
}

// DoneWithCurrent dispatches to the task or batch completion flow based on the
// role's configured receive mode.
func DoneWithCurrent(out io.Writer, workDir, root, role string) error {
	mode, err := project.ReceiveMode(root, role)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	switch mode {
	case "batch":
		return DoneWithCurrentBatch(out, workDir)
	case "task":
		return DoneWithCurrentTask(out, workDir)
	default:
		return handoff.Errorf(2, "INVALID_RECEIVE_MODE: %s for role %s", mode, role)
	}
}

// ReadyForNextTask resumes an in-process task or accepts the highest-priority
// queued task into in_process.
func ReadyForNextTask(out io.Writer, workDir string) error {
	p := paths(workDir)
	if err := ensureDirs(p.new, p.inProcess, p.completed); err != nil {
		return err
	}
	batches, err := handoff.BatchDirs(p.inProcess)
	if err != nil {
		return err
	}
	if len(batches) > 0 {
		return handoff.Errorf(2, "TASK_IN_PROCESS_IS_BATCH: use ready_for_next.sh or done_with_current.sh.\n%s", handoff.Bullets(batches))
	}
	files, err := handoff.Files(p.inProcess)
	if err != nil {
		return err
	}
	if len(files) > 1 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: multiple tasks are already in process.\n%s", handoff.Bullets(files))
	}
	if len(files) == 1 {
		return handoff.PrintTask(out, files[0])
	}
	newFiles, err := handoff.Files(p.new)
	if err != nil {
		return err
	}
	if len(newFiles) == 0 {
		fmt.Fprintln(out, "NO_TASK")
		return nil
	}
	source := newFiles[0]
	target := filepath.Join(p.inProcess, filepath.Base(source))
	if exists(target) {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: target in-process file already exists: %s", target)
	}
	if err := os.Rename(source, target); err != nil {
		return err
	}
	if err := handoff.SetHeader(target, "dequeued_at", handoff.Timestamp()); err != nil {
		return err
	}
	return handoff.PrintTask(out, target)
}

// ReadyForNextBatch resumes an in-process batch or groups all queued tasks at
// the highest priority into a new in_process batch.
func ReadyForNextBatch(out io.Writer, workDir string) error {
	p := paths(workDir)
	if err := ensureDirs(p.new, p.inProcess, p.completed); err != nil {
		return err
	}
	files, err := handoff.Files(p.inProcess)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return handoff.Errorf(2, "TASK_IN_PROCESS_IS_SINGLE: use ready_for_next.sh or done_with_current.sh.\n%s", handoff.Bullets(files))
	}
	batches, err := handoff.BatchDirs(p.inProcess)
	if err != nil {
		return err
	}
	if len(batches) > 1 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: multiple batches are already in process.\n%s", handoff.Bullets(batches))
	}
	if len(batches) == 1 {
		return handoff.PrintBatch(out, batches[0])
	}
	newFiles, err := handoff.Files(p.new)
	if err != nil {
		return err
	}
	if len(newFiles) == 0 {
		fmt.Fprintln(out, "NO_TASK")
		return nil
	}
	batchPriority, err := headerOr(newFiles[0], "priority", "50")
	if err != nil {
		return err
	}
	batchDir, err := newBatchDir(p.inProcess)
	if err != nil {
		return err
	}
	var selected []string
	for _, f := range newFiles {
		pr, err := headerOr(f, "priority", "50")
		if err != nil {
			return err
		}
		if pr == batchPriority {
			selected = append(selected, f)
		}
	}
	if err := os.Mkdir(batchDir, 0o755); err != nil {
		return err
	}
	for _, source := range selected {
		target := filepath.Join(batchDir, filepath.Base(source))
		if exists(target) {
			return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: target batch file already exists: %s", target)
		}
		if err := os.Rename(source, target); err != nil {
			return err
		}
		if err := handoff.SetHeader(target, "dequeued_at", handoff.Timestamp()); err != nil {
			return err
		}
	}
	if len(selected) == 0 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: no tasks selected for batch priority %s.", batchPriority)
	}
	return handoff.PrintBatch(out, batchDir)
}

// DoneWithCurrentTask completes the in-process task, then accepts the next task.
func DoneWithCurrentTask(out io.Writer, workDir string) error {
	p := paths(workDir)
	if err := ensureDirs(p.inProcess, p.completed); err != nil {
		return err
	}
	batches, err := handoff.BatchDirs(p.inProcess)
	if err != nil {
		return err
	}
	if len(batches) > 0 {
		return handoff.Errorf(2, "CURRENT_WORK_IS_BATCH: use done_with_current.sh.\n%s", handoff.Bullets(batches))
	}
	files, err := handoff.Files(p.inProcess)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return &handoff.ExitError{Code: 1, Message: "NO_CURRENT_TASK"}
	}
	if len(files) > 1 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: multiple tasks are in process.\n%s", handoff.Bullets(files))
	}
	source := files[0]
	target := filepath.Join(p.completed, filepath.Base(source))
	if err := handoff.SetHeader(source, "completed_at", handoff.Timestamp()); err != nil {
		return err
	}
	if exists(target) {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: completed file already exists: %s", target)
	}
	if err := os.Rename(source, target); err != nil {
		return err
	}
	fmt.Fprintf(out, "COMPLETED: %s\n", target)
	return ReadyForNextTask(out, workDir)
}

// DoneWithCurrentBatch completes the in-process batch, then accepts the next batch.
func DoneWithCurrentBatch(out io.Writer, workDir string) error {
	p := paths(workDir)
	if err := ensureDirs(p.inProcess, p.completed); err != nil {
		return err
	}
	files, err := handoff.Files(p.inProcess)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return handoff.Errorf(2, "CURRENT_WORK_IS_SINGLE_TASK: use done_with_current.sh.\n%s", handoff.Bullets(files))
	}
	batches, err := handoff.BatchDirs(p.inProcess)
	if err != nil {
		return err
	}
	if len(batches) == 0 {
		return &handoff.ExitError{Code: 1, Message: "NO_CURRENT_BATCH"}
	}
	if len(batches) > 1 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: multiple batches are in process.\n%s", handoff.Bullets(batches))
	}
	sourceDir := batches[0]
	batchFiles, err := handoff.Files(sourceDir)
	if err != nil {
		return err
	}
	targetDir := filepath.Join(p.completed, filepath.Base(sourceDir))
	completedAt := handoff.Timestamp()
	if len(batchFiles) == 0 {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: batch contains no tasks: %s", sourceDir)
	}
	if exists(targetDir) {
		return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: completed batch already exists: %s", targetDir)
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		return err
	}
	for _, source := range batchFiles {
		if err := handoff.SetHeader(source, "completed_at", completedAt); err != nil {
			return err
		}
		target := filepath.Join(targetDir, filepath.Base(source))
		if exists(target) {
			return handoff.Errorf(2, "AMBIGUOUS_TASK_STATE: completed batch file already exists: %s", target)
		}
		if err := os.Rename(source, target); err != nil {
			return err
		}
		fmt.Fprintf(out, "COMPLETED: %s\n", target)
	}
	if err := os.Remove(sourceDir); err != nil {
		return err
	}
	fmt.Fprintf(out, "COMPLETED_BATCH: %s\n", targetDir)
	return ReadyForNextBatch(out, workDir)
}

func newBatchDir(inProcess string) (string, error) {
	for suffix := 1; ; suffix++ {
		dir := filepath.Join(inProcess, fmt.Sprintf("batch_%s_%06d", handoff.IDTimestamp(), suffix))
		if !exists(dir) {
			return dir, nil
		}
	}
}

func headerOr(file, field, def string) (string, error) {
	v, ok, err := handoff.FileHeader(file, field)
	if err != nil {
		return "", err
	}
	if !ok {
		return def, nil
	}
	return v, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
