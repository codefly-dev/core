package ciguard

import "maps"

// A bounded, token-bearing metadata read: no checkout, no inherited Git config,
// no fetch and no caller-selected URL. Behaviour is exercised by the service
// image tests; edits require re-attributing this contract, not just its digest.
const publisherAuthorityScript = `set -euo pipefail
if [ "$GITHUB_EVENT_NAME" != workflow_dispatch ]; then
  echo "::error::service images may only be published by workflow_dispatch; refusing to publish." >&2
  exit 1
fi
# No checkout or inherited Git configuration is consulted by this step.
authority_dir=$(mktemp -d "${RUNNER_TEMP}/publisher-authority.XXXXXX")
trap 'rmdir "$authority_dir"' EXIT
cd "$authority_dir"
authorization=$(printf 'x-access-token:%s' "$GH_TOKEN" | base64 | tr -d '\n')
remote_head=$(env -i PATH="$PATH" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
  GIT_CEILING_DIRECTORIES="$RUNNER_TEMP" GIT_TERMINAL_PROMPT=0 \
  git -c core.fsmonitor=false -c core.hooksPath=/dev/null \
  -c "http.${GITHUB_SERVER_URL}/.extraheader=AUTHORIZATION: basic ${authorization}" \
  ls-remote --symref "${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}" HEAD)
authority=$(printf '%s\n' "$remote_head" | awk '$1 == "ref:" && $3 == "HEAD" { print $2 }')
sha=$(printf '%s\n' "$remote_head" | awk '$2 == "HEAD" && $1 != "ref:" { print $1 }')
if [[ "$authority" != refs/heads/* ]] || [[ ! "$sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "::error::cannot resolve this repository's default branch; refusing to publish." >&2
  exit 1
fi
echo "sha=$sha" >> "$GITHUB_OUTPUT"
`

func isPublisherAuthorityStep(step isolatedStep) bool {
	if step.ID != "authority" || step.Uses != "" || step.If != "" || step.ContinueOnError || step.Shell != "bash" || step.WorkingDirectory != "${{ runner.temp }}" || !maps.Equal(step.Env, map[string]string{"GH_TOKEN": "${{ github.token }}"}) {
		return false
	}
	actual, err := executionShell(step.Run)
	if err != nil {
		return false
	}
	expected, err := executionShell(publisherAuthorityScript)
	return err == nil && shellText(actual) == shellText(expected)
}
