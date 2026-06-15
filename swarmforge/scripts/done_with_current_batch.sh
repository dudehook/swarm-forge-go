#!/usr/bin/env zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/handoff-lib.sh"

INBOX_DIR="$(handoff_inbox_dir)"
IN_PROCESS_DIR="$INBOX_DIR/in_process"
COMPLETED_DIR="$INBOX_DIR/completed"

mkdir -p "$IN_PROCESS_DIR" "$COMPLETED_DIR"

in_process_batches=("$IN_PROCESS_DIR"/batch_*(N/))
in_process_files=("$IN_PROCESS_DIR"/*.handoff(N))

if (( ${#in_process_files[@]} > 0 )); then
  echo "CURRENT_WORK_IS_SINGLE_TASK: use done_with_current.sh." >&2
  for file in "${in_process_files[@]}"; do
    echo "- $file" >&2
  done
  exit 2
fi

if (( ${#in_process_batches[@]} == 0 )); then
  echo "NO_CURRENT_BATCH" >&2
  exit 1
fi

if (( ${#in_process_batches[@]} > 1 )); then
  echo "AMBIGUOUS_TASK_STATE: multiple batches are in process." >&2
  for batch_dir in "${in_process_batches[@]}"; do
    echo "- $batch_dir" >&2
  done
  exit 2
fi

source_dir="${in_process_batches[1]}"
batch_files=("$source_dir"/*.handoff(N))
batch_files=("${(@on)batch_files}")
if (( ${#batch_files[@]} == 0 )); then
  echo "AMBIGUOUS_TASK_STATE: batch contains no tasks: $source_dir" >&2
  exit 2
fi

target_dir="$COMPLETED_DIR/${source_dir:t}"
if [[ -e "$target_dir" ]]; then
  echo "AMBIGUOUS_TASK_STATE: completed batch already exists: $target_dir" >&2
  exit 2
fi

mkdir "$target_dir"
completed_at="$(handoff_timestamp)"

for source_file in "${batch_files[@]}"; do
  handoff_set_header "$source_file" "completed_at" "$completed_at"
  target_file="$target_dir/${source_file:t}"
  if [[ -e "$target_file" ]]; then
    echo "AMBIGUOUS_TASK_STATE: completed batch file already exists: $target_file" >&2
    exit 2
  fi
  mv "$source_file" "$target_file"
  echo "COMPLETED: $target_file"
done

rmdir "$source_dir"
echo "COMPLETED_BATCH: $target_dir"
exec "$SCRIPT_DIR/ready_for_next_batch.sh"
