import os
import sys

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.base.v0 import endpoint_pb2
from codefly.base.v0 import module_pb2


def _serialized_options(message, field):
    return message.DESCRIPTOR.fields_by_name[field].GetOptions().SerializeToString()


def test_endpoint_visibility_is_the_models_three_values():
    # The schema's string.in list, as the Python binding carries it: the three
    # visibilities the model defines, and neither former spelling.
    options = _serialized_options(endpoint_pb2.Endpoint, "visibility")
    for value in (b"public", b"private", b"internal"):
        assert value in options, value
    for former in (b"module", b"external"):
        assert former not in options, former


def test_endpoint_location_is_external_or_none():
    options = _serialized_options(endpoint_pb2.Endpoint, "location")
    assert b"external" in options


def test_interface_endpoint_carries_allow_modules_and_exports_public_or_internal():
    entry = module_pb2.InterfaceEndpoint(
        service="accounts", endpoint="grpc", visibility="internal", allow_modules=["payments"]
    )
    decoded = module_pb2.InterfaceEndpoint.FromString(entry.SerializeToString())
    assert list(decoded.allow_modules) == ["payments"]
    options = _serialized_options(module_pb2.InterfaceEndpoint, "visibility")
    for value in (b"public", b"internal"):
        assert value in options, value
    for former in (b"module", b"private", b"external"):
        assert former not in options, former
