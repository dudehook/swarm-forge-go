#!/usr/bin/env zsh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/handoff-lib.sh"

ROLE="$(handoff_role_or_default)"
MODE="$(handoff_role_receive_mode "$ROLE")"

case "$MODE" in
  batch)
    exec "$SCRIPT_DIR/ready_for_next_batch.sh"
    ;;
  task)
    exec "$SCRIPT_DIR/ready_for_next_task.sh"
    ;;
  *)
    echo "INVALID_RECEIVE_MODE: $MODE for role $ROLE" >&2
    exit 2
    ;;
esac
