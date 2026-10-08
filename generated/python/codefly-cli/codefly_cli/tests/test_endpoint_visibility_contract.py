import os
import sys
import unittest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

from codefly.base.v0 import endpoint_pb2
from codefly.base.v0 import module_pb2

# buf.validate's extension numbers and the field numbers inside its rules, as
# the binding serialises them: the Python package does not ship the validate
# descriptors, so the options are read as wire bytes.
_FIELD_RULES_EXT = 1159  # (buf.validate.field)
_MESSAGE_RULES_EXT = 1159  # (buf.validate.message)
_FIELD_RULES_STRING = 14  # FieldRules.string
_STRING_RULES_IN = 10  # StringRules.in
_MESSAGE_RULES_CEL = 3  # MessageRules.cel
_CEL_EXPRESSION = 3  # Rule.expression


def _varint(data, i):
    result, shift = 0, 0
    while True:
        b = data[i]
        i += 1
        result |= (b & 0x7F) << shift
        if not b & 0x80:
            return result, i
        shift += 7


def _fields(data):
    """Yield (field number, wire type, value) over one serialized message; value
    is bytes for length-delimited fields and an int for varints."""
    i = 0
    while i < len(data):
        key, i = _varint(data, i)
        number, wire = key >> 3, key & 7
        if wire == 0:
            value, i = _varint(data, i)
        elif wire == 2:
            length, i = _varint(data, i)
            value, i = data[i : i + length], i + length
        elif wire == 1:
            value, i = data[i : i + 8], i + 8
        elif wire == 5:
            value, i = data[i : i + 4], i + 4
        else:
            raise AssertionError("unexpected wire type %d" % wire)
        yield number, wire, value


def _string_in(message, field):
    """The exact string.in list of a field, in order, from its serialized options."""
    options = message.DESCRIPTOR.fields_by_name[field].GetOptions().SerializeToString()
    allowed = []
    for number, _, rules in _fields(options):
        if number != _FIELD_RULES_EXT:
            continue
        for rule_number, _, string_rules in _fields(rules):
            if rule_number != _FIELD_RULES_STRING:
                continue
            for in_number, _, value in _fields(string_rules):
                if in_number == _STRING_RULES_IN:
                    allowed.append(value.decode())
    return allowed


def _cel_expressions(message):
    """The message-level CEL expressions, from the serialized message options."""
    options = message.DESCRIPTOR.GetOptions().SerializeToString()
    expressions = []
    for number, _, rules in _fields(options):
        if number != _MESSAGE_RULES_EXT:
            continue
        for rule_number, _, cel in _fields(rules):
            if rule_number != _MESSAGE_RULES_CEL:
                continue
            for part_number, _, value in _fields(cel):
                if part_number == _CEL_EXPRESSION:
                    expressions.append(value.decode())
    return expressions


class EndpointVisibilityContractTest(unittest.TestCase):
    """The Python binding carries the schema exactly as the model defines it."""

    def test_endpoint_visibility_is_exactly_the_models_three_values(self):
        self.assertEqual(["public", "private", "internal"], _string_in(endpoint_pb2.Endpoint, "visibility"))

    def test_endpoint_location_is_exactly_external_or_none(self):
        self.assertEqual(["", "external"], _string_in(endpoint_pb2.Endpoint, "location"))

    def test_endpoint_exposure_is_independent_of_visibility(self):
        self.assertEqual(["", "none", "public"], _string_in(endpoint_pb2.Endpoint, "exposure"))
        self.assertNotIn("allow_modules", endpoint_pb2.Endpoint.DESCRIPTOR.fields_by_name)
        entry = endpoint_pb2.Endpoint(visibility="internal", exposure="public")
        restored = endpoint_pb2.Endpoint.FromString(entry.SerializeToString())
        self.assertEqual("internal", restored.visibility)
        self.assertEqual("public", restored.exposure)

    def test_interface_export_names_no_consumers(self):
        self.assertEqual(["public", "internal"], _string_in(module_pb2.InterfaceEndpoint, "visibility"))
        self.assertNotIn("allow_modules", module_pb2.InterfaceEndpoint.DESCRIPTOR.fields_by_name)
        entry = module_pb2.InterfaceEndpoint(service="accounts", endpoint="grpc", visibility="internal")
        restored = module_pb2.InterfaceEndpoint.FromString(entry.SerializeToString())
        self.assertEqual("internal", restored.visibility)


if __name__ == "__main__":
    unittest.main()
