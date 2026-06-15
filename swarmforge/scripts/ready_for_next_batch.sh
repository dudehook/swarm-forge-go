#!/usr/bin/env zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/handoff-lib.sh"

INBOX_DIR="$(handoff_inbox_dir)"
NEW_DIR="$INBOX_DIR/new"
IN_PROCESS_DIR="$INBOX_DIR/in_process"
COMPLETED_DIR="$INBOX_DIR/completed"

mkdir -p "$NEW_DIR" "$IN_PROCESS_DIR" "$COMPLETED_DIR"

in_process_batches=("$IN_PROCESS_DIR"/batch_*(N/))
in_process_files=("$IN_PROCESS_DIR"/*.handoff(N))

if (( ${#in_process_files[@]} > 0 )); then
  echo "TASK_IN_PROCESS_IS_SINGLE: use ready_for_next.sh or done_with_current.sh." >&2
  for file in "${in_process_files[@]}"; do
    echo "- $file" >&2
  done
  exit 2
fi

if (( ${#in_process_batches[@]} > 1 )); then
  echo "AMBIGUOUS_TASK_STATE: multiple batches are already in process." >&2
  for batch_dir in "${in_process_batches[@]}"; do
    echo "- $batch_dir" >&2
  done
  exit 2
fi

if (( ${#in_process_batches[@]} == 1 )); then
  handoff_print_batch "${in_process_batches[1]}"
  exit 0
fi

new_files=("$NEW_DIR"/*.handoff(N))
if (( ${#new_files[@]} == 0 )); then
  echo "NO_TASK"
  exit 0
fi

new_files=("${(@on)new_files}")
first_file="${new_files[1]}"
batch_priority="$(handoff_header_field priority "$first_file" || echo "50")"
batch_timestamp="$(handoff_id_timestamp)"
batch_suffix=1
while :; do
  batch_dir="$IN_PROCESS_DIR/batch_${batch_timestamp}_$(printf '%06d' "$batch_suffix")"
  if [[ ! -e "$batch_dir" ]]; then
    break
  fi
  batch_suffix=$((batch_suffix + 1))
done

mkdir "$batch_dir"

selected_count=0
for source_file in "${new_files[@]}"; do
  priority="$(handoff_header_field priority "$source_file" || echo "50")"
  if [[ "$priority" != "$batch_priority" ]]; then
    continue
  fi

  target_file="$batch_dir/${source_file:t}"
  if [[ -e "$target_file" ]]; then
    echo "AMBIGUOUS_TASK_STATE: target batch file already exists: $target_file" >&2
    exit 2
  fi

  mv "$source_file" "$target_file"
  handoff_set_header "$target_file" "dequeued_at" "$(handoff_timestamp)"
  selected_count=$((selected_count + 1))
done

if (( selected_count == 0 )); then
  echo "AMBIGUOUS_TASK_STATE: no tasks selected for batch priority $batch_priority." >&2
  exit 2
fi

handoff_print_batch "$batch_dir"
