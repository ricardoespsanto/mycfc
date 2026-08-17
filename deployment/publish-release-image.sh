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

verify_image_revision() {
	digest=$1
	image="$ECR_REPOSITORY@$digest"
	docker pull "$image"
	revision=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")
	if [ "$revision" != "$GIT_SHA" ]; then
		printf '%s\n' "refusing to promote $image: revision label is $revision, expected $GIT_SHA" >&2
		exit 1
	fi
}

git_tag="git-$GIT_SHA"
git_image="$ECR_REPOSITORY:$git_tag"
if digest=$(image_digest_for_tag "$git_tag"); then
	printf '%s\n' "Reusing existing immutable image $git_image@$digest"
else
	printf '%s\n' "Building immutable image $git_image"
	docker build --label "org.opencontainers.image.revision=$GIT_SHA" -t "$git_image" .
	if docker push "$git_image"; then
		digest=$(image_digest_for_tag "$git_tag")
	else
		if digest=$(image_digest_for_tag "$git_tag"); then
			printf '%s\n' "Immutable image $git_image was published concurrently"
		else
			printf '%s\n' "failed to publish immutable image $git_image" >&2
			exit 1
		fi
	fi
fi

verify_image_revision "$digest"
release_tag="release-$(date -u +%Y%m%d%H%M%S)-$GIT_SHA"
if release_digest=$(image_digest_for_tag "$release_tag"); then
	if [ "$release_digest" != "$digest" ]; then
		printf '%s\n' "refusing release-tag collision: $release_tag resolves to $release_digest, expected $digest" >&2
		exit 1
	fi
	printf '%s\n' "Reusing existing immutable release tag $release_tag"
else
	docker tag "$ECR_REPOSITORY@$digest" "$ECR_REPOSITORY:$release_tag"
	if ! docker push "$ECR_REPOSITORY:$release_tag"; then
		if release_digest=$(image_digest_for_tag "$release_tag") && [ "$release_digest" = "$digest" ]; then
			printf '%s\n' "Release tag $release_tag was published concurrently with the same digest"
		else
			printf '%s\n' "failed to publish immutable release tag $release_tag" >&2
			exit 1
		fi
	fi
fi

printf 'image=%s@%s\nregistry=%s\nrelease_tag=%s\nsha=%s\n' "$ECR_REPOSITORY" "$digest" "${ECR_REPOSITORY%%/*}" "$release_tag" "$GIT_SHA" >> "$GITHUB_OUTPUT"
