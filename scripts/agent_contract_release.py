"""Render the agent contract comparison for a Core release."""

import argparse
import json
from pathlib import Path
import re
import subprocess
import tempfile

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
             f"Startup protocol version: **{current['startupProtocolVersion']}**.",
             "Server capabilities: " + ", ".join(f"`{cap}`" for cap in sorted(capabilities)) + ".", ""]
    if previous is None:
        lines.append("Contract introduced; earlier releases did not declare a protocol. "
                     f"Agents must adopt protocol v{current['protocolVersion']} once before a host enforcing this contract can use them.")
    elif current["protocolVersion"] != previous["protocolVersion"]:
        lines.append(f"Protocol changed from {previous['protocolVersion']} in {previous_tag}. "
                     "Hosts and agents must adopt the same protocol generation before use.")
    elif current["startupProtocolVersion"] != previous.get("startupProtocolVersion", 0):
        lines.append(f"Startup protocol changed from {previous.get('startupProtocolVersion', 0)} in {previous_tag}. "
                     "Hosts and agents must adopt the same startup protocol before discovery can succeed.")
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
    operations = set(current.get("operationContracts", []))
    if operations:
        lines.extend(["", "Host-supported operation contracts: " +
                      ", ".join(f"`{name}`" for name in sorted(operations)) + ".",
                      "This is not an executor advertisement. Selected executors must implement and "
                      "advertise the required operation contract before dispatch."])
    lines.extend(["", "The CLI selects required capabilities for each operation. "
                  "A matching protocol does not replace capability checks or this run's recovery-scope acknowledgement.", ""])
    return "\n".join(lines)


def publication_steps(release):
    if release is None:
        return ("create-draft", "upload", "verify", "publish")
    if release["draft"]:
        return ("upload", "verify", "publish")
    if not any(asset["name"] == "contract.json" for asset in release["assets"]):
        raise ValueError("published release is missing contract.json; refusing to report success")
    return ("verify",)


def publish_release(repo, version):
    tag = f"v{version}"
    listing = subprocess.run(
        ["gh", "api", "--paginate", "--slurp", f"repos/{repo}/releases?per_page=100"],
        check=True, capture_output=True, text=True,
    )
    release = next((item for page in json.loads(listing.stdout) for item in page
                    if item["tag_name"] == tag), None)
    with tempfile.TemporaryDirectory() as directory:
        notes = Path(directory) / "notes.md"
        notes.write_text(release_notes(version))
        for step in publication_steps(release):
            if step == "create-draft":
                subprocess.run(["gh", "release", "create", tag, "--repo", repo,
                                "--verify-tag", "--draft", "--title", tag,
                                "--notes-file", str(notes)], check=True)
            elif step == "upload":
                subprocess.run(["gh", "release", "upload", tag, MANIFEST,
                                "--repo", repo, "--clobber"], check=True)
            elif step == "verify":
                asset = subprocess.run(["gh", "release", "download", tag,
                                        "--repo", repo, "--pattern", "contract.json",
                                        "--output", "-"], check=True, capture_output=True)
                if asset.stdout != Path(MANIFEST).read_bytes():
                    raise ValueError("release contract.json differs from the tagged manifest")
            elif step == "publish":
                subprocess.run(["gh", "release", "edit", tag, "--repo", repo,
                                "--draft=false", "--notes-file", str(notes)], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--publish", action="store_true")
    parser.add_argument("--repo")
    args = parser.parse_args()
    if args.publish:
        if not args.repo:
            parser.error("--publish requires --repo")
        publish_release(args.repo, args.version)
    else:
        print(release_notes(args.version), end="")
