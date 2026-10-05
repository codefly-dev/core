import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.base.v0 import endpoint_pb2
from codefly.base.v0 import module_pb2


def _serialized_options(message, field):
    return message.DESCRIPTOR.fields_by_name[field].GetOptions().SerializeToString()


class EndpointVisibilityContractTest(unittest.TestCase):
    """The Python binding carries the schema as the model defines it: three
    visibilities and neither former spelling, a location that is external or
    none, and an interface entry with its allow_modules and its two exportable
    visibilities."""

    def test_endpoint_visibility_is_the_models_three_values(self):
        options = _serialized_options(endpoint_pb2.Endpoint, "visibility")
        for value in (b"public", b"private", b"internal"):
            self.assertIn(value, options)
        for former in (b"module", b"external"):
            self.assertNotIn(former, options)

    def test_endpoint_location_is_external_or_none(self):
        options = _serialized_options(endpoint_pb2.Endpoint, "location")
        self.assertIn(b"external", options)

    def test_interface_endpoint_carries_allow_modules_and_exports_public_or_internal(self):
        entry = module_pb2.InterfaceEndpoint(
            service="accounts", endpoint="grpc", visibility="internal", allow_modules=["payments"]
        )
        decoded = module_pb2.InterfaceEndpoint.FromString(entry.SerializeToString())
        self.assertEqual(list(decoded.allow_modules), ["payments"])
        options = _serialized_options(module_pb2.InterfaceEndpoint, "visibility")
        for value in (b"public", b"internal"):
            self.assertIn(value, options)
        for former in (b"module", b"private", b"external"):
            self.assertNotIn(former, options)


if __name__ == "__main__":
    unittest.main()
