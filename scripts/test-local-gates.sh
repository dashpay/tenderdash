#!/bin/sh
set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
mkdir -p "$fixture/test" "$fixture/bin"
cp "$repo_dir/Makefile" "$fixture/Makefile"
touch "$fixture/test/Makefile"
cd "$fixture"
git init -q
git -c user.name=Test -c user.email=test@example.invalid commit -qm initial --allow-empty

# Exercise the real recipes without downloading tools or building native code.
cat >bin/go <<'EOF'
#!/bin/sh
set -eu
test "$CGO_ENABLED" = 1
case "$CGO_CXXFLAGS" in *bls-signatures/src/include*) ;; *) exit 1 ;; esac
case "$CGO_LDFLAGS" in *-ldashbls*) ;; *) exit 1 ;; esac
printf '%s\n' "$*" >> "$GATE_LOG"
EOF
chmod +x bin/go
PATH="$fixture/bin:$PATH"
GATE_LOG="$fixture/gates.log"
export PATH GATE_LOG
unset LINT_BASE CGO_ENABLED CGO_CXXFLAGS CGO_LDFLAGS

expect_lint_base() {
	: > "$GATE_LOG"
	make --no-print-directory lint "$@" > output.log 2>&1 || { cat output.log; return 1; }
	grep -F -- "--new-from-rev=$expected_base" "$GATE_LOG"
}

expect_lint_failure() {
	: > "$GATE_LOG"
	if make --no-print-directory lint "$@" > output.log 2>&1; then
		echo 'lint unexpectedly succeeded' >&2
		exit 1
	fi
	test ! -s "$GATE_LOG"
}

git update-ref refs/remotes/origin/v1.9-dev HEAD
git update-ref refs/remotes/origin/v1.10-dev HEAD
expected_base=origin/v1.10-dev
expect_lint_base

git update-ref refs/remotes/origin/v2.0-dev HEAD
expected_base=origin/v2.0-dev
expect_lint_base

expected_base=origin/v1.9-dev
expect_lint_base LINT_BASE="$expected_base"
LINT_BASE="$expected_base" expect_lint_base

expect_lint_failure LINT_BASE=origin/missing
grep -F 'not found' output.log
expect_lint_failure LINT_BASE=

unrelated=$(printf 'unrelated\n' | git -c user.name=Test -c user.email=test@example.invalid commit-tree 'HEAD^{tree}')
git update-ref refs/remotes/origin/unrelated "$unrelated"
expect_lint_failure LINT_BASE=origin/unrelated
grep -F 'no common history' output.log

git update-ref -d refs/remotes/origin/v1.9-dev
git update-ref -d refs/remotes/origin/v1.10-dev
git update-ref -d refs/remotes/origin/v2.0-dev
expect_lint_failure
grep -F 'no development branch found' output.log
expected_base=HEAD
expect_lint_base LINT_BASE=HEAD

make --no-print-directory vet lint-all > output.log 2>&1
grep -Fx 'vet ./...' "$GATE_LOG"
grep -E 'golangci-lint@[^ ]+ run --timeout 10m$' "$GATE_LOG"
echo 'Local quality gate tests passed'
