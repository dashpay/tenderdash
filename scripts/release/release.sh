#!/usr/bin/env bash

set -eo pipefail

function success {
    [[ -t 1 ]] && echo -e "\e[32mSUCCESS:\e[0m" "$@" || echo "SUCCESS:" "$@"
}

function debug {
    [[ -t 1 ]] && echo -e "\e[93mDEBUG:\e[0m" "$@" || echo "DEBUG:" "$@"
}

function error {
    debug Error: "$@"
    [[ -t 1 ]] && echo -e '\e[91mERROR:\e[0m' "$@" || echo "ERROR:" "$@"
    cleanup
    exit 1
}
function displayHelp {
    cat <<EOF
# $(basename "$0")

This script prepares new release of Tenderdash. It:

* generates changelog
* creates a release branch (release_VERSION)
* creates pull request with changelog

Once the pull request is accepted, you still need to tag and create a release manually.

To use, you need to checkout your current development branch (like 'v0.7-dev') first.

## Usage

    $0 [flags] <platform|tenderdash>

where flags can be one of:
    -r=<x.y.z-dev.n>, --release=<x.y.z-dev.n> - release number, like 0.7.0 (REQUIRED)
    --cleanup - clean up before releasing; it can remove your local changes
    -C=<path> - path to local Tenderdash repository
    -s, --sign - generate signed binaries
    --no-wait, --stop-after-pr
        Run through opening the release PR, print 'RELEASE_PR=<url>' on a
        machine-parseable line, then exit 0. Skips the blocking merge-wait
        and the draft-release step. Implies --non-interactive.
    --finalize, --create-release
        Skip prep/changelog/version/branch/PR. Instead verify the
        release_<ver> PR is MERGED (error if not), then create the DRAFT
        GitHub release. Idempotent: if the draft or tag already exists,
        reports it without failing. Implies --non-interactive.
        Emits 'RELEASE_DRAFT=<url>' on stdout.
        Requires a Tenderdash git checkout (any branch), or
        GH_REPO=dashpay/tenderdash so gh can resolve the repository.
    --non-interactive, --yes
        Assume "yes" to every confirmation prompt; never block on stdin.
        Automatically implied by --no-wait and --finalize.
    --dry-run
        Validate, generate a changelog preview, and compute the version
        bump; print what would happen without taking any remote, commit,
        push, PR, tag, or release action. Restores the working tree on
        exit. Implies --non-interactive. Safe to use as a pre-check.
    -h, --help - display this help message

## Examples

### Full release of 0.7.4

git checkout v0.7-dev
$0  --release=0.7.4

### Prerelease of 0.8.0-dev.3

git checkout v0.8-dev
$0  --release=0.8.0-dev.3

### Agent/CI two-call flow (non-blocking)

# Step 1: prepare changelog, branch, and open PR — then exit immediately.
git checkout v0.8-dev
$0 --release=0.8.0-dev.3 --no-wait
# Output includes: RELEASE_PR=https://github.com/...

# Step 2: after a human (or CI check) merges the PR, finalize the release.
# Must run from within the Tenderdash git checkout, or set GH_REPO=dashpay/tenderdash.
$0 --release=0.8.0-dev.3 --finalize
# Output includes: RELEASE_DRAFT=https://github.com/...

EOF
}

function configureDefaults {
    debug Configuring default values
    REPO_DIR="$(realpath "$(dirname "${0}")/../..")"
}

function parseArgs {
    debug Parsing command line
    while [[ "$#" -ge 1 ]]; do
        # for arg in "$@"; do
        arg="$1"
        case ${arg} in
        --cleanup)
            CLEANUP=yes
            shift
            ;;
        -r=* | --release=*)
            NEW_PACKAGE_VERSION="${arg#*=}"
            shift
            ;;
        -r | --release)
            shift
            if [[ -n "$1" ]]; then
                NEW_PACKAGE_VERSION="${1#*=}"
            fi
            shift
            ;;
        -C=*)
            REPO_DIR="${arg#*=}"
            shift
            ;;
        -C)
            shift
            REPO_DIR="${1#*=}"
            shift
            ;;
        -h | --help)
            displayHelp
            shift
            exit 0
            ;;
        -s | --sign)
            SIGN=1
            shift 1
            ;;
        --no-wait | --stop-after-pr)
            NO_WAIT=yes
            shift
            ;;
        --finalize | --create-release)
            FINALIZE=yes
            shift
            ;;
        --non-interactive | --yes)
            NON_INTERACTIVE=yes
            shift
            ;;
        --dry-run)
            DRY_RUN=yes
            shift
            ;;
        *)
            error "Unrecoginzed command line argument '${arg}';  try '$0 --help'"
            ;;
        esac
    done
}

function configureFinal() {
    debug Finalizing configuration
    VERSION_WITHOUT_PRERELEASE=${NEW_PACKAGE_VERSION%-*}

    if [[ "${VERSION_WITHOUT_PRERELEASE}" == "${NEW_PACKAGE_VERSION}" ]]; then
        ## Full release
        RELEASE_TYPE=release
    else
        RELEASE_TYPE=prerelease
    fi

    CURRENT_BRANCH="$(git branch --show-current)"
    SOURCE_BRANCH="v${VERSION_WITHOUT_PRERELEASE%.*}-dev"
    RELEASE_BRANCH="release_${NEW_PACKAGE_VERSION}"
    MILESTONE="v${VERSION_WITHOUT_PRERELEASE%.*}"

    if [[ ${RELEASE_TYPE} != "prerelease" ]]; then # full release
        TARGET_BRANCH="master"
    else # prerelease
        TARGET_BRANCH="v${VERSION_WITHOUT_PRERELEASE%.*}-dev"
    fi

    debug "Repository: ${REPO_DIR}"
    debug "Release type: ${RELEASE_TYPE}"
    debug "New version: ${NEW_PACKAGE_VERSION}"
    debug "Source branch: ${SOURCE_BRANCH}"
    debug "Target branch: ${TARGET_BRANCH}"
    debug "Flags: NO_WAIT=${NO_WAIT:-no} FINALIZE=${FINALIZE:-no} NON_INTERACTIVE=${NON_INTERACTIVE:-no} SIGN=${SIGN:-no} DRY_RUN=${DRY_RUN:-no}"
}

function validate {
    debug Validating configuration
    if [[ -z "${NEW_PACKAGE_VERSION}" ]]; then
        error "You must provide new release version with --release=x.y.z; see '$0 --help' for more details"
    fi

    if [[ "${CURRENT_BRANCH}" != "${SOURCE_BRANCH}" ]]; then
        error "you must run this script from the \"${SOURCE_BRANCH}\" branch"
    fi

    local UNCOMMITTED_FILES
    UNCOMMITTED_FILES="$(git status -su)"
    if [[ -n "${UNCOMMITTED_FILES}" ]]; then
        error "Commit or stash your changes before running this script"
    fi

    # ensure github authentication
    if ! gh auth status &>/dev/null; then
        error "Not authenticated to GitHub; run 'gh auth login' first"
    fi

    # Ensure local branch is in sync with origin before generating the changelog,
    # so commits on origin are not silently missed.
    git fetch origin "${SOURCE_BRANCH}"
    if [[ "$(git rev-parse HEAD)" != "$(git rev-parse "origin/${SOURCE_BRANCH}")" ]]; then
        git merge --ff-only "origin/${SOURCE_BRANCH}" ||
            error "Your ${SOURCE_BRANCH} is out of sync with origin; sync it before releasing"
    fi
}

# validateFinalize performs lightweight validation needed for --finalize mode:
# version is set, gh is authenticated, and the GitHub repo is resolvable.
# No working-tree or branch checks (--finalize never modifies the checkout),
# but gh still resolves the repo via the local git remote or GH_REPO env var.
function validateFinalize {
    debug Validating configuration for finalize
    if [[ -z "${NEW_PACKAGE_VERSION}" ]]; then
        error "You must provide new release version with --release=x.y.z; see '$0 --help' for more details"
    fi

    if ! gh auth status &>/dev/null; then
        error "Not authenticated to GitHub; run 'gh auth login' first"
    fi

    # Confirm gh can resolve the target repo.  This requires either a git
    # checkout whose origin remote points to dashpay/tenderdash, or GH_REPO
    # set to dashpay/tenderdash.  A missing repo context produces opaque
    # errors from subsequent gh calls — fail fast with an actionable message.
    if ! gh repo view &>/dev/null; then
        error "Cannot resolve GitHub repository. Run from within the Tenderdash git checkout (any branch), or set GH_REPO=dashpay/tenderdash."
    fi
}

# preflight runs early checks before any mutating or remote step in the
# prepare path.  In --dry-run the push probe is informational (non-fatal).
function preflight() {
    debug "Running preflight checks"

    # Docker must be reachable — git-cliff runs inside a container.
    if ! docker info &>/dev/null; then
        error "Docker is not reachable — git-cliff requires Docker to generate the changelog. Start Docker and retry."
    fi
    debug "Preflight: Docker OK"

    # GitHub CLI must be authenticated.
    if ! gh auth status &>/dev/null; then
        error "Not authenticated to GitHub; run 'gh auth login' first"
    fi
    debug "Preflight: gh auth OK"

    # Push-credential probe: dry-run push to the release branch we will create.
    # Catches missing credentials or token scope before any commit is made.
    local push_out
    if ! push_out="$(git push --dry-run origin "HEAD:refs/heads/${RELEASE_BRANCH}" 2>&1)"; then
        local msg="Push credential probe failed for origin/${RELEASE_BRANCH}. Check your SSH key or token (an elevated token may be required for protected branches). Output: ${push_out}"
        if [[ -n "${DRY_RUN}" ]]; then
            debug "DRY-RUN (informational, non-fatal): ${msg}"
        else
            error "${msg}"
        fi
    else
        debug "Preflight: push probe to origin/${RELEASE_BRANCH} OK"
    fi
}

# preflightFinalize is the lightweight preflight for --finalize mode.
# Finalize is API-only (no Docker, no git push); only gh auth is checked.
function preflightFinalize() {
    debug "Running preflight checks (finalize mode)"
    if ! gh auth status &>/dev/null; then
        error "Not authenticated to GitHub; run 'gh auth login' first"
    fi
    debug "Preflight: gh auth OK"
}

function generateChangelog {
    debug Generating CHANGELOG

    CLIFF_CONFIG="${REPO_DIR}/scripts/release/cliff.toml"
    CLIFF_ARGS=()
    if [[ "${RELEASE_TYPE}" = "prerelease" ]]; then
        CLIFF_ARGS+=(--ignore-tags 'v[0-9]\.[0-9]+\.[0-9]+-[a-z]+\.[0-9]+')
    fi

    docker run --rm \
        -v "${REPO_DIR}/.git":/app/.git:ro \
        -v "${CLIFF_CONFIG}":/cliff.toml:ro \
        -v "${REPO_DIR}/CHANGELOG.md":/CHANGELOG.md \
        orhunp/git-cliff:2.4.0 \
        --config /cliff.toml \
        --output /CHANGELOG.md \
        --tag "v${NEW_PACKAGE_VERSION}" \
        "${CLIFF_ARGS[@]}" \
        --strip all \
        --verbose \
        'v1.0.0-dev.1..HEAD'
}

function updateVersionGo {
    sed -i'' -e "s/TMVersionDefault = \"[^\"]*\"\s*\$/TMVersionDefault = \"${NEW_PACKAGE_VERSION}\"/g" "${REPO_DIR}/version/version.go"
}

function createReleasePR {
    debug "Creating release branch ${RELEASE_BRANCH}"
    git checkout -q -b "${RELEASE_BRANCH}"

    # commit changes
    git commit -m "chore(release): update changelog and version to ${NEW_PACKAGE_VERSION}" \
        "${REPO_DIR}/CHANGELOG.md" \
        "${REPO_DIR}/version/version.go"

    # push changes
    git push --force -u origin "${RELEASE_BRANCH}"

    debug "Creating milestone ${MILESTONE} if it doesn't exist yet"
    # {owner}/{repo} are substituted by gh from the current repo; HTTP 422 means it already exists.
    gh api --silent --method POST 'repos/{owner}/{repo}/milestones' --field "title=${MILESTONE}" || true

    if [[ -n "$(getPrURL)" ]]; then
        debug "PR for branch ${TARGET_BRANCH} already exists, skipping creation"
    else
        debug "Creating PR for branch ${TARGET_BRANCH}"
        gh pr create --base "${TARGET_BRANCH}" \
            --fill \
            --title "chore(release): update changelog and bump version to ${NEW_PACKAGE_VERSION}" \
            --body-file "${REPO_DIR}/scripts/release/pr_description.md" \
            --milestone "${MILESTONE}"
    fi
}

function getPrURL() {
    gh pr list --json url --jq '.[0].url' -H "${RELEASE_BRANCH}" -B "${TARGET_BRANCH}"
}

function getPrState() {
    gh pr list --json state --jq .[0].state -H "${RELEASE_BRANCH}" -B "${TARGET_BRANCH}" --state all
}

function waitForMerge() {
    debug 'Waiting for the PR to be merged; use ^C to cancel'

    while [[ "$(getPrState)" != "MERGED" ]]; do
        sleep 5
    done
}

function createRelease() {
    gh_args=""
    if [[ "${RELEASE_TYPE}" = "prerelease" ]]; then
        gh_args=--prerelease
    fi

    gh release create \
        --draft \
        --title "v${NEW_PACKAGE_VERSION}" \
        --generate-notes \
        --target "${TARGET_BRANCH}" \
        ${gh_args} \
        "v${NEW_PACKAGE_VERSION}"
}

# createReleaseIdempotent creates the draft GitHub release if it does not yet
# exist. If a release (draft or published) already exists for the tag, it
# reports the URL and returns successfully without re-creating anything.
# Always emits the machine-parseable line: RELEASE_DRAFT=<url>
function createReleaseIdempotent() {
    if gh release view "v${NEW_PACKAGE_VERSION}" &>/dev/null; then
        local existing_url
        existing_url="$(getReleaseUrl)"
        debug "Release v${NEW_PACKAGE_VERSION} already exists; skipping creation"
        echo "RELEASE_DRAFT=${existing_url}"
        return 0
    fi

    createRelease
    echo "RELEASE_DRAFT=$(getReleaseUrl)"
}

function deleteRelease() {
    if [[ "$(gh release view --json isDraft --jq .isDraft "v${NEW_PACKAGE_VERSION}")" == "true" ]]; then
        gh release delete "v${NEW_PACKAGE_VERSION}"
    fi

    git tag --delete "v${NEW_PACKAGE_VERSION}" || true
    git push --delete origin "v${NEW_PACKAGE_VERSION}" || true
}

function getReleaseUrl() {
    gh release view --json url --jq .url "v${NEW_PACKAGE_VERSION}"
}

function waitForRelease() {
    debug 'Waiting for release to be published; use ^C to cancel'

    while [[ "$(gh release view --json isDraft --jq .isDraft "v${NEW_PACKAGE_VERSION}")" != "false" ]]; do
        sleep 10
    done
}

function buildAndUploadArtifacts() {

    bindir="$(mktemp -d)"
    local platforms=("linux/amd64" "linux/arm64")

    # The build checks out the release tag (detached HEAD); restore the branch on
    # any exit so the developer is never left stranded.
    trap 'git checkout -q "${SOURCE_BRANCH}" 2>/dev/null || true' EXIT

    waitForRelease

    # Build signed binaries from the released tag, not the working checkout.
    git fetch --tags
    git checkout "v${NEW_PACKAGE_VERSION}"

    buildBinaries "${bindir}" "${platforms[@]}"

    # Sign binaries
    signBinary "${bindir}"/tenderdash-*

    # Create tarball
    for platform in "${platforms[@]}"; do
        local platform_safe="${platform//\//-}"

        tar -C "${bindir}" \
            -czf "${bindir}/tenderdash-${NEW_PACKAGE_VERSION}-${platform_safe}.tar.gz" \
            "tenderdash-${platform_safe}" "tenderdash-${platform_safe}.sig"
    done

    pushd "${bindir}"
    sha256sum *.tar.gz >SHA256SUMS
    popd

    # Upload to release
    uploadBinaries "${bindir}"

    # Cleanup
    rm -r "${bindir}"
}

# buildBinaries <destdir> <platform1> <platform2> ...
function buildBinaries() {
    local dest_dir="$1"
    shift
    if [[ -z "${dest_dir}" ]]; then
        error "Destination directory is required to build binaries"
    fi

    debug Building binaries
    pushd "$(realpath "$(dirname "${0}")/../..")"

    while [ -n "$1" ]; do
        platform="$1"
        shift

        local platform_safe="${platform//\//-}"

        debug "Building binaries for ${platform}"
        make clean
        docker buildx build \
            --platform "${platform}" \
            --build-arg TENDERMINT_BUILD_OPTIONS='tenderdash,stable' \
            -f DOCKER/Dockerfile \
            -t tenderdash-local:"v${NEW_PACKAGE_VERSION}-${platform_safe}" \
            --load \
            .
        # Copy /usr/bin/tenderdash from image to dest_dir
        docker create --name tenderdash-local-"${platform_safe}" --platform "${platform}" tenderdash-local:"v${NEW_PACKAGE_VERSION}-${platform_safe}"
        docker cp tenderdash-local-"${platform_safe}":/usr/bin/tenderdash "${dest_dir}/tenderdash-${platform_safe}"
        docker rm tenderdash-local-"${platform_safe}"
        # Remove the image
        docker rmi tenderdash-local:"v${NEW_PACKAGE_VERSION}-${platform_safe}"
    done

    popd
}

# Sign tenederdash binary using default gpg key
#
# The key can be overridden by setting GPG_KEY_ID environment variable
#
# Args: list of binaries to sign
function signBinary() {

    local gpg_cmd="gpg"
    if [[ -n "${GPG_KEY_ID}" ]]; then
        gpg_cmd="gpg --local-user=${GPG_KEY_ID}"
    fi

    for binary in "$@"; do
        debug "Signing binaries with GPG ${gpg_cmd}"
        $gpg_cmd --armor "--output=${binary}.sig" --detach-sign "${binary}"
    done
}

function uploadBinaries() {
    local bin_dir="$1"

    debug uploading artifacts to release "v${NEW_PACKAGE_VERSION}"
    gh release upload --clobber "v${NEW_PACKAGE_VERSION}" "${bin_dir}"/{*.tar.gz,SHA256SUMS}
}

function cleanup() {
    debug Cleaning up

    # In --finalize or --dry-run mode the working tree is never modified by
    # this script, so there is nothing to restore and we must not clobber
    # the caller's checkout (branch switch, branch deletion, make clean).
    if [[ -n "${FINALIZE}" || -n "${DRY_RUN}" ]]; then
        return 0
    fi

    git checkout --quiet -- "${REPO_DIR}/CHANGELOG.md"
    git checkout --quiet "${SOURCE_BRANCH}" || true
    git branch --quiet -D "${RELEASE_BRANCH}" || true

    # We need to re-detect current branch again
    CURRENT_BRANCH="$(git branch --show-current)"

    make clean || true
}

# ---------------------------------------------------------------------------
# Main entry point
# ---------------------------------------------------------------------------

configureDefaults
parseArgs "$@"

# --no-wait, --finalize, and --dry-run imply non-interactive.
# Set this BEFORE configureFinal so the debug output reflects the actual state.
if [[ -n "${NO_WAIT}" || -n "${FINALIZE}" || -n "${DRY_RUN}" ]]; then
    NON_INTERACTIVE=yes
fi

# Mutual-exclusivity guard: --no-wait and --finalize express opposite intents.
if [[ -n "${NO_WAIT}" && -n "${FINALIZE}" ]]; then
    error "--no-wait and --finalize are mutually exclusive; use --no-wait for step 1, then --finalize for step 2"
fi

# Normalize REPO_DIR to an absolute path (handles -C relative/path) and cd
# into it so all subsequent git/gh/make commands operate against the intended
# checkout regardless of the caller's working directory.  This is especially
# important for CI/agent invocations via an absolute script path.
REPO_DIR="$(realpath "${REPO_DIR}")"
cd "${REPO_DIR}"

configureFinal

# ---------------------------------------------------------------------------
# --finalize / --create-release: skip prep, verify PR merged, create release
# ---------------------------------------------------------------------------
if [[ -n "${FINALIZE}" ]]; then
    preflightFinalize
    validateFinalize

    pr_state="$(getPrState)"
    if [[ "${pr_state}" != "MERGED" ]]; then
        error "Release PR for ${RELEASE_BRANCH} → ${TARGET_BRANCH} is not merged yet (state: ${pr_state:-unknown}). Merge it first, then re-run with --finalize."
    fi

    success "Release PR for ${NEW_PACKAGE_VERSION} is merged. Creating draft release."
    createReleaseIdempotent

    success "Release ${NEW_PACKAGE_VERSION} draft created (or already existed)."
    exit 0
fi

# ---------------------------------------------------------------------------
# --dry-run: preview validate+changelog without any remote/commit action
# ---------------------------------------------------------------------------
if [[ -n "${DRY_RUN}" ]]; then
    # Restore CHANGELOG.md on any exit (normal or error) so the tree stays clean.
    trap 'git checkout --quiet -- "${REPO_DIR}/CHANGELOG.md" 2>/dev/null || true' EXIT

    preflight   # push probe is informational/non-fatal in DRY_RUN
    validate
    local_version="$(grep 'TMVersionDefault' "${REPO_DIR}/version/version.go" | \
        sed 's/.*TMVersionDefault = "\([^"]*\)".*/\1/')"
    generateChangelog
    echo "DRY-RUN: TMVersionDefault: ${local_version} -> ${NEW_PACKAGE_VERSION}"
    echo "DRY-RUN: RELEASE_PR=<would open PR: ${RELEASE_BRANCH} -> ${TARGET_BRANCH}>"
    echo "DRY-RUN: RELEASE_DRAFT=<would create: v${NEW_PACKAGE_VERSION}>"
    git checkout --quiet -- "${REPO_DIR}/CHANGELOG.md"
    trap - EXIT
    success "DRY-RUN complete — no remote or commit actions were performed."
    exit 0
fi

# ---------------------------------------------------------------------------
# Standard prep flow (shared by default interactive mode and --no-wait)
# ---------------------------------------------------------------------------

if [[ -n "${CLEANUP}" ]]; then
    cleanup
    deleteRelease
fi

preflight
validate
generateChangelog
updateVersionGo
createReleasePR

PR_URL="$(getPrURL)"

success "New release branch ${RELEASE_BRANCH} for ${NEW_PACKAGE_VERSION} prepared successfully."
success "Release PR: ${PR_URL}"
# Machine-parseable marker — agents/CI grep for this line.
echo "RELEASE_PR=${PR_URL}"

# ---------------------------------------------------------------------------
# --no-wait / --stop-after-pr: exit after opening the PR
# ---------------------------------------------------------------------------
if [[ -n "${NO_WAIT}" ]]; then
    cleanup
    success "Stopping before merge wait (--no-wait)."
    success "Merge the PR, then run: $0 --release=${NEW_PACKAGE_VERSION} --finalize"
    exit 0
fi

# ---------------------------------------------------------------------------
# Interactive / default: block until merged, then create draft release
# ---------------------------------------------------------------------------

success "Please review it and merge."

if [[ "${RELEASE_TYPE}" = "prerelease" ]]; then
    success "NOTE: Use 'squash and merge' approach."
else
    success "NOTE: Use 'create merge commit' approach."
fi

waitForMerge

success "Release branch ${RELEASE_BRANCH} for ${NEW_PACKAGE_VERSION} is merged. Preparing the release."
createRelease

DRAFT_URL="$(getReleaseUrl)"
# Machine-parseable marker — agents/CI grep for this line.
echo "RELEASE_DRAFT=${DRAFT_URL}"

cleanup

sleep 5 # wait for the release to be finalized

success "Release ${NEW_PACKAGE_VERSION} created successfully."
success "Accept it at: ${DRAFT_URL}"

if [ -n "${SIGN}"  ];then
    buildAndUploadArtifacts
fi
