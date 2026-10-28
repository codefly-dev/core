# Python companion

The Python runtime companion uses uv to create environments and install
dependencies. It keeps debugpy in the system interpreter because the DAP
companion launches `python -m debugpy.adapter`. Services mount their environment
at `/venv`; `UV_PROJECT_ENVIRONMENT` and `PATH` select it at runtime.

Version 0.0.8 removes build-only pip after installing debugpy, including pip's
installed vendored dependencies and Python's bundled ensurepip bootstrap wheel.
Both copies in 0.0.7 contain the same vulnerable dependencies; upgrading a
standalone urllib3, setuptools or msgpack package does not replace those copies.
The current upstream pip 26.2.2 wheel still contains them.

Use `uv venv` and `uv pip` for environment management. This image does not offer
`pip` or `python -m ensurepip`; ordinary `python -m venv` bootstrap behavior
must not be assumed. Debugpy, uv, and their package metadata are retained.
No vendor manifest or SBOM is rewritten to conceal installed code.
Codefly's editable-library and Python-upgrade helpers still invoke pip in their
calling process. Their ordinary host behavior is unchanged; invoking those
pip-dependent commands inside this companion is not a supported replacement
for the host workflow. This image change does not redefine host interpreter
selection.

Both architecture builds verify pip/ensurepip absence, the debugpy adapter
entrypoint, and uv installation/import in a fresh environment. The tagged
`python_companion_required` DAP test exercises initialize and launch through
Core's maintained client against the selected companion. Publication
uses the [companion procedure](../../docs/runbooks/publish-companions.md).
Downstream security clearance requires rebuilding services and scanning their
exact image digests; a source change or companion tag is not that evidence.
Removal affects the merged runtime filesystem; historical immutable base-layer
blobs still contain their original bytes.

The DAP client sends launch/attach, waits for the adapter's `initialized`
event, completes configuration, and then collects the start response. Debugpy
defers that response until `configurationDone`; waiting for it first deadlocks.
The connection probe also preserves early adapter telemetry bytes so the
protocol reader receives complete frames.
