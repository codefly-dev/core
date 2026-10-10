import pathlib
import unittest

from codefly.runnable.v0 import projection_pb2
from google.protobuf import json_format


class ToolProjectionContractTest(unittest.TestCase):
    def test_both_json_spellings_round_trip_every_projection_field(self):
        root = pathlib.Path(__file__).resolve().parents[5]
        fixtures = root / "runnable" / "testdata" / "tool-projection"
        values = []
        for spelling in ("snake_case", "lowerCamelCase"):
            with self.subTest(spelling=spelling):
                value = json_format.Parse(
                    (fixtures / (spelling + ".json")).read_text(),
                    projection_pb2.ToolProjection(),
                )
                self.assert_all_fields_populated(value)
                encoded = json_format.MessageToJson(value, preserving_proto_field_name=True)
                self.assertEqual(
                    value, json_format.Parse(encoded, projection_pb2.ToolProjection())
                )
                self.assertEqual(
                    value, projection_pb2.ToolProjection.FromString(value.SerializeToString())
                )
                values.append(value)
        self.assertEqual(values[0], values[1])

    def assert_all_fields_populated(self, value):
        populated = {field.name for field, _ in value.ListFields()}
        self.assertEqual(
            {field.name for field in value.DESCRIPTOR.fields}, populated,
            "The fixture must populate every field, including new schema fields",
        )
        for field in value.DESCRIPTOR.fields:
            if field.message_type is None or not field.message_type.full_name.startswith("codefly.runnable.v0."):
                continue
            children = getattr(value, field.name)
            if not field.is_repeated:
                children = [children]
            for child in children:
                self.assert_all_fields_populated(child)


if __name__ == "__main__":
    unittest.main()
