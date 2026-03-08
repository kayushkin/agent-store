#!/bin/bash
# Deploy a git branch to a dev preview slot on the server
#
# Usage:
#   devdeploy.sh deploy <slot_id> <repo_url> <branch> [project]
#   devdeploy.sh stop <slot_id>
#   devdeploy.sh status
#   devdeploy.sh logs <slot_id>
#
# Example:
#   devdeploy.sh deploy 1 git@github.com:kayushkin/kayushkin.com.git agent/brigid/feat-nav kayushkin
#   devdeploy.sh deploy 2 git@github.com:kayushkin/kayushkin.com.git agent/brigid/new-css kayushkin
#
# Requires:
#   - SSH access to server (uses ~/bin/ssh-kcom.sh or DEPLOY_SSH_CMD)
#   - Git repo accessible from server
#   - Go installed on server (for building)

set -e

# Config
DEPLOY_HOST="${DEPLOY_HOST:-kayushkin.com}"
DEPLOY_USER="${DEPLOY_USER:-kayushkincom}"
DEPLOY_DIR="${DEPLOY_DIR:-dev}"          # ~/dev/ on server
BASE_PORT=9000

# SSH command — use ssh-kcom.sh if available, fall back to env
ssh_cmd() {
    if [ -f ~/bin/ssh-kcom.sh ]; then
        # ssh-kcom.sh runs the command directly
        ~/bin/ssh-kcom.sh "$1"
    elif [ -n "$DEPLOY_SSH_CMD" ]; then
        eval "$DEPLOY_SSH_CMD \"$1\""
    else
        source ~/bin/.env 2>/dev/null
        sshpass -p "$KAYUSHKINCOM_PASS" ssh "$DEPLOY_USER@$DEPLOY_HOST" "$1"
    fi
}

cmd_deploy() {
    local slot_id="$1"
    local repo_url="$2"
    local branch="$3"
    local project="${4:-kayushkin}"

    if [ -z "$slot_id" ] || [ -z "$repo_url" ] || [ -z "$branch" ]; then
        echo "Usage: devdeploy.sh deploy <slot_id> <repo_url> <branch> [project]"
        exit 1
    fi

    local port=$((BASE_PORT + slot_id))
    local slot_dir="$DEPLOY_DIR/slot-$slot_id"

    echo "Deploying slot $slot_id..."
    echo "  Repo:   $repo_url"
    echo "  Branch: $branch"
    echo "  Port:   $port"
    echo "  URL:    http://$slot_id.dev.$DEPLOY_HOST"
    echo ""

    # Stop existing dev server for this slot
    echo "Stopping existing server (if any)..."
    ssh_cmd "
        if [ -f ~/.$DEPLOY_DIR/slot-$slot_id.pid ]; then
            kill \$(cat ~/.$DEPLOY_DIR/slot-$slot_id.pid) 2>/dev/null || true
            rm -f ~/.$DEPLOY_DIR/slot-$slot_id.pid
        fi
    "

    # Clone or update repo on server
    echo "Setting up repo..."
    ssh_cmd "
        mkdir -p ~/$slot_dir ~/.$DEPLOY_DIR

        if [ -d ~/$slot_dir/.git ]; then
            cd ~/$slot_dir
            git fetch origin
            git checkout $branch 2>/dev/null || git checkout -b $branch origin/$branch
            git reset --hard origin/$branch
        else
            rm -rf ~/$slot_dir
            git clone --branch $branch --single-branch $repo_url ~/$slot_dir
        fi
    "

    # Build backend
    echo "Building backend..."
    ssh_cmd "
        cd ~/$slot_dir
        export PATH=\$HOME/.local/share/mise/shims:\$PATH
        go build -o kayushkin-server . 2>&1
    "

    # Build frontend (if package.json exists)
    echo "Building frontend..."
    ssh_cmd "
        cd ~/$slot_dir
        if [ -f frontend/package.json ]; then
            cd frontend
            export PATH=\$HOME/.local/share/mise/shims:\$PATH
            npm install --silent 2>&1
            npm run build 2>&1
            # Copy build output
            rm -rf ~/$slot_dir/build/assets/*
            cp -r dist/* ~/$slot_dir/build/ 2>/dev/null || true
        fi
    "

    # Start dev server
    echo "Starting dev server on port $port..."
    ssh_cmd "
        cd ~/$slot_dir
        nohup ./kayushkin-server -port $port -build ./build \
            > ~/.$DEPLOY_DIR/slot-$slot_id.log 2>&1 &
        echo \$! > ~/.$DEPLOY_DIR/slot-$slot_id.pid
        echo \"Started (pid=\$(cat ~/.$DEPLOY_DIR/slot-$slot_id.pid))\"
    "

    echo ""
    echo "✅ Deployed: http://$slot_id.dev.$DEPLOY_HOST"
}

cmd_stop() {
    local slot_id="$1"
    if [ -z "$slot_id" ]; then
        echo "Usage: devdeploy.sh stop <slot_id>"
        exit 1
    fi

    echo "Stopping slot $slot_id..."
    ssh_cmd "
        if [ -f ~/.$DEPLOY_DIR/slot-$slot_id.pid ]; then
            kill \$(cat ~/.$DEPLOY_DIR/slot-$slot_id.pid) 2>/dev/null && echo 'Stopped' || echo 'Already stopped'
            rm -f ~/.$DEPLOY_DIR/slot-$slot_id.pid
        else
            echo 'No server running for slot $slot_id'
        fi
    "
}

cmd_status() {
    echo "Dev Preview Slots"
    echo "================="
    ssh_cmd "
        mkdir -p ~/.$DEPLOY_DIR
        for pidfile in ~/.$DEPLOY_DIR/slot-*.pid; do
            [ -f \"\$pidfile\" ] || continue
            slot=\$(basename \"\$pidfile\" .pid | sed 's/slot-//')
            pid=\$(cat \"\$pidfile\")
            port=\$((9000 + slot))
            if kill -0 \"\$pid\" 2>/dev/null; then
                # Get branch name
                branch=''
                if [ -d ~/$DEPLOY_DIR/slot-\$slot/.git ]; then
                    branch=\$(cd ~/$DEPLOY_DIR/slot-\$slot && git branch --show-current 2>/dev/null)
                fi
                echo \"  slot-\$slot: running (pid=\$pid, port=\$port, branch=\$branch) → \$slot.dev.$DEPLOY_HOST\"
            else
                echo \"  slot-\$slot: dead (was pid=\$pid)\"
                rm -f \"\$pidfile\"
            fi
        done
        # Check for unused slots
        ls ~/.$DEPLOY_DIR/slot-*.pid 2>/dev/null | wc -l | grep -q '^0$' && echo '  No dev servers running'
    "
}

cmd_logs() {
    local slot_id="$1"
    if [ -z "$slot_id" ]; then
        echo "Usage: devdeploy.sh logs <slot_id>"
        exit 1
    fi
    ssh_cmd "tail -50 ~/.$DEPLOY_DIR/slot-$slot_id.log 2>/dev/null || echo 'No logs for slot $slot_id'"
}

case "${1:-status}" in
    deploy)  cmd_deploy "$2" "$3" "$4" "$5" ;;
    stop)    cmd_stop "$2" ;;
    status)  cmd_status ;;
    logs)    cmd_logs "$2" ;;
    *)
        echo "Usage: devdeploy.sh {deploy|stop|status|logs}"
        echo ""
        echo "  deploy <slot> <repo_url> <branch> [project]"
        echo "  stop <slot>"
        echo "  status"
        echo "  logs <slot>"
        exit 1
        ;;
esac
