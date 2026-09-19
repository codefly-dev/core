"""Render the agent contract comparison for a Core release."""

import argparse
import json
from pathlib import Path
import re
import subprocess

MANIFEST = "agents/contract/contract.json"


def git(*args):
    return subprocess.run(["git", *args], check=True, text=True, capture_output=True).stdout.strip()


def release_notes(version):
    current = json.loads(Path(MANIFEST).read_text())
    tags = git("tag", "--merged", "HEAD", "--sort=-version:refname").splitlines()
    previous_tag = next((tag for tag in tags if re.fullmatch(r"v\d+\.\d+\.\d+", tag)
                         and tag != f"v{version}"), None)
    previous = None
    if previous_tag and git("ls-tree", "--name-only", previous_tag, "--", MANIFEST):
        previous = json.loads(git("show", f"{previous_tag}:{MANIFEST}"))

    capabilities = set(current["capabilities"])
    lines = ["## CLI-agent compatibility", "",
             f"Protocol version: **{current['protocolVersion']}** (independent of the Core module version).",
             "Server capabilities: " + ", ".join(f"`{cap}`" for cap in sorted(capabilities)) + ".", ""]
    if previous is None:
        lines.append("Contract introduced; earlier releases did not declare a protocol. "
                     f"Agents must adopt protocol v{current['protocolVersion']} once before a host enforcing this contract can use them.")
    elif current["protocolVersion"] != previous["protocolVersion"]:
        lines.append(f"Protocol changed from {previous['protocolVersion']} in {previous_tag}. "
                     "Hosts and agents must adopt the same protocol generation before use.")
    else:
        added = capabilities - set(previous["capabilities"])
        removed = set(previous["capabilities"]) - capabilities
        if not added and not removed:
            lines.append(f"Contract unchanged from {previous_tag}. No agent rebuild is required solely for this Core bump.")
        else:
            lines.append(f"Protocol unchanged from {previous_tag}; capabilities changed.")
            if added:
                lines.append("Added: " + ", ".join(f"`{cap}`" for cap in sorted(added)) + ".")
            if removed:
                lines.append("Removed: " + ", ".join(f"`{cap}`" for cap in sorted(removed)) + ".")
            lines.append("Republish agents only when a run requires capabilities they do not advertise; "
                         "hosts requiring removed capabilities cannot use this advertisement.")
    lines.extend(["", "The CLI selects required capabilities for each operation. "
                  "A matching protocol does not replace capability checks or this run's recovery-scope acknowledgement.", ""])
    return "\n".join(lines)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    print(release_notes(args.version), end="")
