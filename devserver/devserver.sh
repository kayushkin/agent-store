#!/bin/bash
# Dev preview server manager
# Ties into the worktree pool — starts/stops dev servers per slot
#
# Usage:
#   devserver.sh start <slot_id> <build_path> [port]
#   devserver.sh stop <slot_id>
#   devserver.sh status
#   devserver.sh stopall

PIDDIR="${HOME}/.config/agent-store/dev-pids"
LOGDIR="${HOME}/.config/agent-store/dev-logs"
BASE_PORT=9000

mkdir -p "$PIDDIR" "$LOGDIR"

cmd_start() {
    local slot_id="$1"
    local build_path="$2"
    local port="${3:-$((BASE_PORT + slot_id))}"

    if [ -z "$slot_id" ] || [ -z "$build_path" ]; then
        echo "Usage: devserver.sh start <slot_id> <build_path> [port]"
        exit 1
    fi

    # Kill existing if running
    cmd_stop "$slot_id" 2>/dev/null

    # Check if kayushkin-server binary exists in the build path
    local server_bin=""
    if [ -f "$build_path/kayushkin-server" ]; then
        server_bin="$build_path/kayushkin-server"
    elif [ -f "$build_path/main.go" ]; then
        # Build it
        echo "Building server from $build_path..."
        (cd "$build_path" && go build -o kayushkin-server .) || {
            echo "Build failed"
            exit 1
        }
        server_bin="$build_path/kayushkin-server"
    else
        echo "No server binary or main.go found in $build_path"
        exit 1
    fi

    echo "Starting dev server slot-$slot_id on port $port..."
    nohup "$server_bin" -port "$port" -build "$build_path/build" \
        > "$LOGDIR/slot-$slot_id.log" 2>&1 &
    
    local pid=$!
    echo "$pid" > "$PIDDIR/slot-$slot_id.pid"
    echo "Started slot-$slot_id (pid=$pid, port=$port)"
    echo "  Preview: http://$slot_id.dev.kayushkin.com"
    echo "  Log: $LOGDIR/slot-$slot_id.log"
}

cmd_stop() {
    local slot_id="$1"
    local pidfile="$PIDDIR/slot-$slot_id.pid"

    if [ ! -f "$pidfile" ]; then
        echo "No running server for slot-$slot_id"
        return 1
    fi

    local pid
    pid=$(cat "$pidfile")
    if kill "$pid" 2>/dev/null; then
        echo "Stopped slot-$slot_id (pid=$pid)"
    else
        echo "Process $pid already dead"
    fi
    rm -f "$pidfile"
}

cmd_status() {
    echo "Dev Preview Servers"
    echo "==================="
    local found=0
    for pidfile in "$PIDDIR"/slot-*.pid; do
        [ -f "$pidfile" ] || continue
        found=1
        local slot_id
        slot_id=$(basename "$pidfile" .pid | sed 's/slot-//')
        local pid
        pid=$(cat "$pidfile")
        local port=$((BASE_PORT + slot_id))
        
        if kill -0 "$pid" 2>/dev/null; then
            echo "  slot-$slot_id: running (pid=$pid, port=$port) → $slot_id.dev.kayushkin.com"
        else
            echo "  slot-$slot_id: dead (was pid=$pid)"
            rm -f "$pidfile"
        fi
    done
    
    if [ $found -eq 0 ]; then
        echo "  No dev servers running"
    fi
}

cmd_stopall() {
    for pidfile in "$PIDDIR"/slot-*.pid; do
        [ -f "$pidfile" ] || continue
        local slot_id
        slot_id=$(basename "$pidfile" .pid | sed 's/slot-//')
        cmd_stop "$slot_id"
    done
}

case "${1:-status}" in
    start)   cmd_start "$2" "$3" "$4" ;;
    stop)    cmd_stop "$2" ;;
    status)  cmd_status ;;
    stopall) cmd_stopall ;;
    *)
        echo "Usage: devserver.sh {start|stop|status|stopall}"
        exit 1
        ;;
esac
