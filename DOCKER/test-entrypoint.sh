#!/bin/bash
# Checks how docker-entrypoint.sh forwards signals and reports tenderdash's exit
# status, using a fake tenderdash on PATH.
set -euo pipefail

entrypoint="$(cd "$(dirname "$0")" && pwd)/docker-entrypoint.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/home/config"

# fake_tenderdash BODY: the fake runs BODY, then idles in an interruptible wait,
# so its traps run as soon as a signal arrives.
fake_tenderdash() {
	printf '#!/bin/bash\n%s\nwhile :; do sleep 60 & wait $!; done\n' "$1" >"$work/bin/tenderdash"
	chmod +x "$work/bin/tenderdash"
}

# run_entrypoint ARG SIGNALS: starts the entrypoint, sends SIGTERM SIGNALS times
# (0.3 s apart), and prints its exit status.
run_entrypoint() {
	local pid status=0
	rm -f "$work/cleaned"
	# A wedged run fails instead of hanging CI. --foreground keeps timeout from
	# signalling the fake tenderdash itself; --preserve-status passes on the
	# entrypoint's status after timeout forwards our SIGTERM to it.
	PATH="$work/bin:$PATH" TMHOME="$work/home" MARKER="$work/cleaned" \
		timeout --foreground --preserve-status -k 5 60 bash "$entrypoint" "$1" >/dev/null 2>&1 &
	pid=$!
	sleep 0.3
	for ((i = 0; i < $2; i++)); do
		kill -TERM "$pid"
		sleep 0.3
	done
	wait "$pid" || status=$?
	echo "$status"
}

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

# Tenderdash finishes a slow shutdown before the entrypoint exits, and its
# exit status is kept.
# shellcheck disable=SC2016 # expanded by the fake tenderdash
fake_tenderdash 'trap "sleep 1; touch \"\$MARKER\"; exit 7" TERM'
status=$(run_entrypoint start 1)
[ -f "$work/cleaned" ] || fail "entrypoint exited before tenderdash finished its shutdown"
[ "$status" = 7 ] || fail "slow shutdown: want status 7, got $status"

# A second signal during the shutdown does not end the wait.
status=$(run_entrypoint start 2)
[ -f "$work/cleaned" ] || fail "second signal: entrypoint exited before tenderdash finished"
[ "$status" = 7 ] || fail "second signal: want status 7, got $status"

# Tenderdash exiting at once on the signal still reports its own status.
fake_tenderdash 'trap "exit 0" TERM'
for _ in $(seq 20); do
	status=$(run_entrypoint start 1)
	[ "$status" = 0 ] || fail "immediate exit after signal: want status 0, got $status"
done

# Without a signal, the entrypoint returns tenderdash's status.
printf '#!/bin/bash\nexit 3\n' >"$work/bin/tenderdash"
status=$(run_entrypoint version 0)
[ "$status" = 3 ] || fail "plain exit: want status 3, got $status"

echo "docker-entrypoint.sh: all checks passed"
