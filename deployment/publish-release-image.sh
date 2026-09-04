#!/bin/sh
set -eu

: "${AWS_REGION:?AWS_REGION is required}"
: "${ECR_REPOSITORY:?ECR_REPOSITORY is required}"
: "${ECR_REPOSITORY_NAME:?ECR_REPOSITORY_NAME is required}"
: "${GIT_SHA:?GIT_SHA is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

case "$GIT_SHA" in
	[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
	*) printf '%s\n' "GIT_SHA must be a 40-character lowercase Git SHA" >&2; exit 1 ;;
esac

image_digest_for_tag() {
	tag=$1
	if output=$(aws ecr describe-images --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY_NAME" --image-ids imageTag="$tag" --query 'imageDetails[0].imageDigest' --output text 2>&1); then
		case "$output" in
			sha256:*) printf '%s\n' "$output"; return 0 ;;
			*) printf '%s\n' "unexpected ECR digest for $tag: $output" >&2; exit 1 ;;
		esac
	fi
	case "$output" in
		*ImageNotFoundException*) return 1 ;;
		*) printf '%s\n' "$output" >&2; exit 1 ;;
	esac
}

write_output() {
	printf '%s\n' "$1" >>"$GITHUB_OUTPUT"
}

prepare() {
	git_tag="git-$GIT_SHA"
	release_tag="release-$(date -u +%Y%m%d%H%M%S)-$GIT_SHA"

	write_output "repository=$ECR_REPOSITORY"
	write_output "git_tag=$git_tag"
	write_output "release_tag=$release_tag"
	write_output "sha=$GIT_SHA"
	if digest=$(image_digest_for_tag "$git_tag"); then
		printf '%s\n' "Reusing existing immutable image $ECR_REPOSITORY:$git_tag@$digest"
		write_output 'build_required=false'
		write_output "image_digest=$digest"
	else
		write_output 'build_required=true'
	fi
}

verify_image_revision() {
	digest=$1
	image="$ECR_REPOSITORY@$digest"
	revision=$(docker buildx imagetools inspect --format '{{index .Image.Config.Labels "org.opencontainers.image.revision"}}' "$image")
	if [ "$revision" != "$GIT_SHA" ]; then
		printf '%s\n' "refusing to promote $image: revision label is $revision, expected $GIT_SHA" >&2
		exit 1
	fi
}

promote_release_tag() {
	image_manifest=$(aws ecr batch-get-image --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY_NAME" --image-ids imageTag="$git_tag" --accepted-media-types application/vnd.oci.image.manifest.v1+json application/vnd.docker.distribution.manifest.v2+json --query 'images[0].imageManifest' --output text)
	case "$image_manifest" in
		''|None)
			printf '%s\n' "unable to read immutable image manifest for $git_tag" >&2
			exit 1
			;;
	esac
	if aws ecr put-image --region "$AWS_REGION" --repository-name "$ECR_REPOSITORY_NAME" --image-tag "$release_tag" --image-manifest "$image_manifest" >/dev/null; then
		return 0
	fi
	if release_digest=$(image_digest_for_tag "$release_tag") && [ "$release_digest" = "$IMAGE_DIGEST" ]; then
		printf '%s\n' "Release tag $release_tag was published concurrently with the same digest"
		return 0
	fi
	printf '%s\n' "failed to publish immutable release tag $release_tag" >&2
	exit 1
}

case "${1:-}" in
	prepare)
		prepare
		exit 0
		;;
	'')
		: "${IMAGE_DIGEST:?IMAGE_DIGEST is required}"
		: "${RELEASE_TAG:?RELEASE_TAG is required}"
		;;
	*)
		printf '%s\n' "usage: $0 [prepare]" >&2
		exit 2
		;;
esac

case "$IMAGE_DIGEST" in
	sha256:[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
	*) printf '%s\n' "IMAGE_DIGEST must be a sha256 digest" >&2; exit 1 ;;
esac

git_tag="git-$GIT_SHA"
release_tag=$RELEASE_TAG
if git_digest=$(image_digest_for_tag "$git_tag"); then
	if [ "$git_digest" != "$IMAGE_DIGEST" ]; then
		printf '%s\n' "refusing to promote $ECR_REPOSITORY:$git_tag: digest is $git_digest, expected $IMAGE_DIGEST" >&2
		exit 1
	fi
else
	printf '%s\n' "immutable image tag $git_tag was not published" >&2
	exit 1
fi

verify_image_revision "$IMAGE_DIGEST"
if release_digest=$(image_digest_for_tag "$release_tag"); then
	if [ "$release_digest" != "$IMAGE_DIGEST" ]; then
		printf '%s\n' "refusing release-tag collision: $release_tag resolves to $release_digest, expected $IMAGE_DIGEST" >&2
		exit 1
	fi
	printf '%s\n' "Reusing existing immutable release tag $release_tag"
else
	promote_release_tag
fi

printf 'image=%s@%s\nregistry=%s\nrelease_tag=%s\nsha=%s\n' "$ECR_REPOSITORY" "$IMAGE_DIGEST" "${ECR_REPOSITORY%%/*}" "$release_tag" "$GIT_SHA" >> "$GITHUB_OUTPUT"
