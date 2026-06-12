#!/usr/bin/env zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/handoff-lib.sh"

usage() {
  echo "Usage: receive-handoff.sh --file <message-file> [--receiver <receiver-role>]" >&2
}

MESSAGE_FILE=""
RECEIVER_ARG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --file)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      MESSAGE_FILE="$2"
      shift 2
      ;;
    --receiver)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      RECEIVER_ARG="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$MESSAGE_FILE" || ! -f "$MESSAGE_FILE" ]]; then
  echo "Message file not found: $MESSAGE_FILE" >&2
  exit 1
fi

RECEIVER="$(handoff_role_or_default "$RECEIVER_ARG")"
NORMALIZED_FILE="$(handoff_temp_file "receive-handoff")"
awk '
  !found {
    header = "message type: handoff"
    start = index($0, header)
    if (start == 0) {
      header = "message type: resend-request"
      start = index($0, header)
    }
    if (start > 0) {
      found = 1
      print substr($0, start)
    }
    next
  }
  { print }
' "$MESSAGE_FILE" > "$NORMALIZED_FILE"
if [[ ! -s "$NORMALIZED_FILE" ]]; then
  echo "No valid protocol message found" >&2
  exit 2
fi

MESSAGE="$(< "$NORMALIZED_FILE")"
MESSAGE_TYPE="$(handoff_field "message type" "$NORMALIZED_FILE")" || {
  echo "Missing message type" >&2
  exit 2
}
MESSAGE_ID="$(handoff_field "message id" "$NORMALIZED_FILE")" || {
  echo "Missing message id" >&2
  exit 2
}
SENDER="$(handoff_field "sender role" "$NORMALIZED_FILE")" || {
  echo "Missing sender role" >&2
  exit 2
}
TARGET="$(handoff_field "target role" "$NORMALIZED_FILE")" || {
  echo "Missing target role" >&2
  exit 2
}
SEQUENCE="$(handoff_field "message sequence" "$NORMALIZED_FILE")" || {
  echo "Missing message sequence" >&2
  exit 2
}
MESSAGE_PRIORITY="$(handoff_field "message priority" "$NORMALIZED_FILE" || echo "50")"

if [[ "$TARGET" != "$RECEIVER" ]]; then
  echo "Message target '$TARGET' does not match receiver '$RECEIVER'" >&2
  exit 2
fi

if ! handoff_valid_message_id "$MESSAGE_ID"; then
  echo "Invalid message id: $MESSAGE_ID" >&2
  exit 2
fi

if ! handoff_valid_priority "$MESSAGE_PRIORITY"; then
  echo "Invalid message priority: $MESSAGE_PRIORITY" >&2
  exit 2
fi

if [[ "$MESSAGE_TYPE" == "handoff" ]]; then
  BRANCH_NAME="$(handoff_field "branch name" "$NORMALIZED_FILE")" || {
    echo "Missing branch name" >&2
    exit 2
  }
  COMMIT_HASH="$(handoff_field "commit hash" "$NORMALIZED_FILE")" || {
    echo "Missing commit hash" >&2
    exit 2
  }
  if [[ "$COMMIT_HASH" != [0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f] ]]; then
    echo "Invalid commit hash: $COMMIT_HASH" >&2
    exit 2
  fi
fi

STREAM="${SENDER}-${TARGET}"
seq_num="$(handoff_sequence_number "$SEQUENCE")"
last_file="$(handoff_last_received_file "$STREAM")"
if [[ -f "$last_file" ]]; then
  last="$(< "$last_file")"
  has_last=1
else
  last=0
  has_last=0
fi
if [[ ! "$last" == <-> ]]; then
  last=0
fi
expected=$((last + 1))

if [[ "$MESSAGE_TYPE" != "handoff" && "$MESSAGE_TYPE" != "resend-request" ]]; then
  echo "Unknown message type: $MESSAGE_TYPE" >&2
  exit 2
fi

if (( has_last == 0 )); then
  expected="$seq_num"
fi

if (( seq_num > expected )); then
  missing_range="$(handoff_sequence_range "$expected" "$seq_num")"
  handoff_archive_received "$STREAM" "$SEQUENCE" "$MESSAGE" "out-of-order"
  handoff_append_logbook "queued" "$MESSAGE" "do not process; missing $STREAM sequences $missing_range"
  request_file="$(handoff_temp_file "resend-request")"
  cat > "$request_file" <<EOF
resend stream: $STREAM
resend sender role: $SENDER
resend target role: $TARGET
resend sequences: $missing_range
reason: received $SEQUENCE while last processed was $(printf '%06d' "$last")
EOF
  send-handoff.sh "$SENDER" --type resend-request --file "$request_file" --sender "$RECEIVER"
  echo "DO NOT PROCESS $MESSAGE_ID; requested resend of $STREAM $missing_range"
  exit 20
fi

if (( seq_num < expected )); then
  handoff_archive_received "$STREAM" "$SEQUENCE" "$MESSAGE" "duplicate"
  handoff_append_logbook "queued" "$MESSAGE" "do not process; duplicate or stale $STREAM sequence $SEQUENCE"
  echo "DO NOT PROCESS $MESSAGE_ID; duplicate or stale sequence"
  exit 21
fi

if [[ "$MESSAGE_TYPE" == "resend-request" ]]; then
  handoff_archive_received "$STREAM" "$SEQUENCE" "$MESSAGE" "processed"
  handoff_append_logbook "received" "$MESSAGE" "received resend request $MESSAGE_ID"
  printf '%06d\n' "$seq_num" > "$last_file"
  resend_stream="$(handoff_field "resend stream" "$NORMALIZED_FILE")" || {
    echo "Missing resend stream" >&2
    exit 2
  }
  resend_sequences="$(handoff_field "resend sequences" "$NORMALIZED_FILE")" || {
    echo "Missing resend sequences" >&2
    exit 2
  }
  resend_target="$(handoff_field "resend target role" "$NORMALIZED_FILE")" || {
    echo "Missing resend target role" >&2
    exit 2
  }
  resend-handoff.sh --stream "$resend_stream" --sequences "$resend_sequences" --target "$resend_target"
  echo "RESENT requested handoffs for $resend_stream $resend_sequences"
  exit 10
fi

if [[ "$MESSAGE_TYPE" == "handoff" ]]; then
  handoff_archive_received "$STREAM" "$SEQUENCE" "$MESSAGE" "processed"
  handoff_append_logbook "received" "$MESSAGE" "received handoff $MESSAGE_ID"
  printf '%06d\n' "$seq_num" > "$last_file"
  queue_file="$(handoff_queue_accepted "$MESSAGE_PRIORITY" "$STREAM" "$SEQUENCE" "$MESSAGE")"
  echo "QUEUED $queue_file"
  exit 0
fi
