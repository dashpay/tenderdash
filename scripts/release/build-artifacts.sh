#!/usr/bin/env bash
# Build and sign local archives from an existing tag; never publish or change refs.
set -euo pipefail

if [[ $# != 4 ]]; then
	echo "usage: $0 REPO TAG REVIEWED_SHA OUTPUT_DIRECTORY" >&2
	exit 1
fi
repo=$(realpath "$1")
tag=$2
reviewed_sha=$3
output=$(realpath -m "$4")
[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-dev\.[0-9]+)?$ ]] || { echo 'invalid release tag' >&2; exit 1; }
[[ $reviewed_sha =~ ^[0-9a-f]{40}$ ]] || { echo 'expected full reviewed commit SHA' >&2; exit 1; }
[[ $(git -C "$repo" rev-parse "refs/tags/$tag^{commit}") == "$reviewed_sha" ]] || {
	echo 'tag does not match reviewed SHA' >&2
	exit 1
}
[[ ! -e $output ]] || { echo 'output directory already exists; refusing to overwrite artifacts' >&2; exit 1; }
mkdir -p "$output"
scratch=$(mktemp -d "$output/.build.XXXXXX")
container_id=''
image=''
cleanup() {
	[[ -z $container_id ]] || docker rm -f "$container_id" >/dev/null 2>&1 || true
	[[ -z $image ]] || docker rmi "$image" >/dev/null 2>&1 || true
	rm -rf "$scratch"
}
trap cleanup EXIT
# A clone has a self-contained .git directory, also inside the Docker context.
git clone --quiet --no-hardlinks --no-tags --no-checkout "$repo" "$scratch/source"
git -C "$scratch/source" fetch --quiet --no-tags "$repo" "refs/tags/$tag:refs/tags/$tag"
[[ $(git -C "$scratch/source" rev-parse "refs/tags/$tag^{commit}") == "$reviewed_sha" ]] || {
	echo 'tag changed while preparing build source' >&2
	exit 1
}
git -C "$scratch/source" checkout --quiet --detach "$reviewed_sha"
git -C "$scratch/source" submodule update --init --recursive
build_options=tenderdash,stable
[[ $tag != *-dev.* ]] || build_options=tenderdash,dev
sign_args=()
[[ -z ${GPG_KEY_ID:-} ]] || sign_args+=(--local-user "$GPG_KEY_ID")
for arch in amd64 arm64; do
	image="tenderdash-release-local:${reviewed_sha:0:12}-$arch-$$"
	docker buildx build --platform "linux/$arch" \
		--build-arg "TENDERMINT_BUILD_OPTIONS=$build_options" \
		-f "$scratch/source/DOCKER/Dockerfile" -t "$image" --load "$scratch/source"
	container_id=$(docker create --platform "linux/$arch" "$image")
	binary="tenderdash-linux-$arch"
	docker cp "$container_id:/usr/bin/tenderdash" "$output/$binary"
	docker rm "$container_id" >/dev/null
	container_id=''
	docker rmi "$image" >/dev/null
	image=''
	gpg "${sign_args[@]}" --armor --output "$output/$binary.sig" --detach-sign "$output/$binary"
	gpg --verify "$output/$binary.sig" "$output/$binary"
	tar -C "$output" -czf "$output/tenderdash-${tag#v}-linux-$arch.tar.gz" "$binary" "$binary.sig"
done
(cd "$output" && sha256sum ./*.tar.gz > SHA256SUMS)
printf '%s\n' "$reviewed_sha" > "$output/SOURCE_COMMIT"
echo "Artifacts prepared in $output; no release assets uploaded."
